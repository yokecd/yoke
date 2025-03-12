package yoke

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/davidmdm/x/xerr"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/oci"
	"github.com/yokecd/yoke/internal/text"
	"github.com/yokecd/yoke/internal/wasi"
)

type Commander struct {
	k8s *k8s.Client
}

func FromKubeConfig(path string) (*Commander, error) {
	client, err := k8s.NewClientFromKubeConfig(path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize k8s client: %w", err)
	}
	return &Commander{client}, nil
}

func FromK8Client(client *k8s.Client) *Commander {
	return &Commander{client}
}

type DescentParams struct {
	Release    string
	RevisionID int
	Namespace  string
	Wait       time.Duration
	Poll       time.Duration
}

func (commander Commander) Descent(ctx context.Context, params DescentParams) error {
	defer internal.DebugTimer(ctx, "descent")()

	targetNS := cmp.Or(params.Namespace, "default")

	release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revisions for release %q: %w", params.Release, err)
	}

	if len(release.History) == 0 {
		return fmt.Errorf("no release found %q in namespace %q", params.Release, targetNS)
	}

	if id := params.RevisionID; id < 1 || id > len(release.History) {
		return fmt.Errorf("requested revision id %d is not within valid range 1 to %d", id, len(release.History))
	}

	targetRevision := release.History[params.RevisionID-1]

	next, err := commander.k8s.GetRevisionResources(ctx, targetRevision)
	if err != nil {
		return fmt.Errorf("failed to lookup target revision resources: %w", err)
	}

	previous, err := commander.k8s.GetRevisionResources(ctx, release.ActiveRevision())
	if err != nil {
		return fmt.Errorf("failed to lookup current revision resources: %w", err)
	}

	previousID := release.ActiveIndex() + 1
	if id := params.RevisionID; id == previousID {
		return fmt.Errorf("can't descend from %d to %d", previousID, id)
	}

	for _, stage := range next {
		if err := commander.k8s.ApplyResources(ctx, stage, k8s.ApplyResourcesOpts{SkipDryRun: true}); err != nil {
			return fmt.Errorf("failed to apply resources: %w", err)
		}
		if params.Wait > 0 {
			if err := commander.k8s.WaitForReadyMany(ctx, stage, k8s.WaitOptions{Timeout: params.Wait, Interval: params.Poll}); err != nil {
				return fmt.Errorf("release did not become ready within wait period: %w", err)
			}
		}
	}

	if err := commander.k8s.UpdateRevisionActiveState(ctx, targetRevision); err != nil {
		return fmt.Errorf("failed to update revision history: %w", err)
	}

	if _, err := commander.k8s.RemoveOrphans(ctx, previous, next); err != nil {
		return fmt.Errorf("failed to remove orphaned resources: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "successful descent of %s from revision %d to %d\n", params.Release, previousID, params.RevisionID)

	return nil
}

func (client Commander) Mayday(ctx context.Context, name, ns string) error {
	defer internal.DebugTimer(ctx, "mayday")()

	targetNS := cmp.Or(ns, "default")

	release, err := client.k8s.GetRelease(ctx, name, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release: %w", err)
	}

	if len(release.History) == 0 {
		return internal.Warning("mayday noop: no history found for release: " + name)
	}

	state, err := client.k8s.GetRevisionResources(ctx, release.ActiveRevision())
	if err != nil {
		return fmt.Errorf("failed to get resources for current revision: %w", err)
	}

	if _, err := client.k8s.RemoveOrphans(ctx, state, nil); err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	if err := client.k8s.DeleteRevisions(ctx, *release); err != nil {
		return fmt.Errorf("failed to delete revision history: %w", err)
	}

	return nil
}

type TurbulenceParams struct {
	Namespace     string
	Release       string
	Context       int
	ConflictsOnly bool
	Fix           bool
	Color         bool
	Silent        bool
}

func (commander Commander) Turbulence(ctx context.Context, params TurbulenceParams) error {
	defer internal.DebugTimer(ctx, "turbulence")()

	targetNS := cmp.Or(params.Namespace, "default")

	if params.Silent {
		ctx = internal.WithStderr(ctx, io.Discard)
	}

	release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revisions for release %s: %w", params.Release, err)
	}

	if len(release.History) == 0 {
		return fmt.Errorf("release not found for %q in namespace %q", params.Release, targetNS)
	}

	stages, err := commander.k8s.GetRevisionResources(ctx, release.ActiveRevision())
	if err != nil {
		return fmt.Errorf("failed to get current resources: %w", err)
	}

	expected := internal.CanonicalMap(stages.Flatten())

	actual := map[string]*unstructured.Unstructured{}
	for name, resource := range expected {
		value, err := commander.k8s.GetInClusterState(ctx, resource)
		if err != nil {
			return fmt.Errorf("failed to get in cluster state for resource %s: %w", internal.Canonical(resource), err)
		}
		if value != nil && params.ConflictsOnly {
			value.Object = removeAdditions(resource.Object, value.Object)
		}
		actual[name] = value
	}

	if params.Fix {
		forceConflicts := k8s.ApplyOpts{ForceConflicts: true}

		var errs []error
		for name, desired := range expected {
			if reflect.DeepEqual(desired, actual[name]) {
				continue
			}
			if err := commander.k8s.ApplyResource(ctx, desired, forceConflicts); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
			fmt.Fprintf(internal.Stderr(ctx), "fixed drift for: %s\n", name)
		}

		return xerr.MultiErrOrderedFrom("failed to apply desired state to drift", errs...)
	}

	expectedFile, err := text.ToYamlFile("expected", expected)
	if err != nil {
		return fmt.Errorf("failed to encode expected state to yaml: %w", err)
	}

	actualFile, err := text.ToYamlFile("actual", actual)
	if err != nil {
		return fmt.Errorf("failed to encode actual state to yaml: %w", err)
	}

	differ := func() text.DiffFunc {
		if params.Color {
			return text.DiffColorized
		}
		return text.Diff
	}()

	diff := differ(expectedFile, actualFile, params.Context)

	if diff == "" {
		return internal.Warning("no turbulence detected")
	}

	_, err = fmt.Fprint(internal.Stdout(ctx), diff)
	return err
}

// removeAdditions compares removes fields from actual that are not in expected.
// it removes the additional properties in place and returns "actual" back.
// Values passed to removeAdditions are expected to be generic json compliant structures:
// map[string]any, []any, or scalars.
func removeAdditions[T any](expected, actual T) T {
	if reflect.ValueOf(expected).Type() != reflect.ValueOf(actual).Type() {
		return actual
	}

	switch a := any(actual).(type) {
	case map[string]any:
		e := any(expected).(map[string]any)
		for key := range a {
			if _, ok := e[key]; !ok {
				delete(a, key)
				continue
			}
			a[key] = removeAdditions(e[key], a[key])
		}
	case []any:
		e := any(expected).([]any)
		for i := range min(len(a), len(e)) {
			a[i] = removeAdditions(e[i], a[i])
		}
	}

	return actual
}

type StowParams struct {
	WasmFile string
	URL      string
	Tags     []string
	Insecure bool
}

func Stow(ctx context.Context, params StowParams) error {
	wasm, err := loadFile(params.WasmFile)
	if err != nil {
		return fmt.Errorf("failed to load wasm file: %w", err)
	}

	if _, err := wasi.Compile(ctx, wasi.CompileParams{Wasm: wasm}); err != nil {
		return fmt.Errorf("invalid wasm module: %w", err)
	}

	digestURL, err := oci.PushArtifact(ctx, oci.PushArtifactParams{
		Data:     wasm,
		URL:      params.URL,
		Insecure: params.Insecure,
		Tags:     params.Tags,
	})
	if err != nil {
		return fmt.Errorf("failed to stow wasm artifact: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "stowed wasm artifact at %s\n", digestURL)

	return nil
}
