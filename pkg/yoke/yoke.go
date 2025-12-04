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
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/oci"
	"github.com/yokecd/yoke/internal/text"
	"github.com/yokecd/yoke/internal/wasi"
)

type Commander struct {
	k8s *k8s.Client
}

func FromKubeConfigFlags(flags *genericclioptions.ConfigFlags) (*Commander, error) {
	client, err := k8s.NewClientFromConfigFlags(flags)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize k8s client: %w", err)
	}
	return &Commander{client}, nil
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
	Lock       bool

	PruneOpts
}

func (commander Commander) Descent(ctx context.Context, params DescentParams) (err error) {
	defer internal.DebugTimer(ctx, "descent")()

	targetNS := cmp.Or(params.Namespace, commander.k8s.DefaultNamespace)

	release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revisions for release %q: %w", params.Release, err)
	}

	if params.Lock {
		if err := commander.k8s.LockRelease(ctx, *release); err != nil {
			return fmt.Errorf("failed to aquire release lock: %w", err)
		}
		defer func() {
			if unlockErr := commander.k8s.UnlockRelease(ctx, *release); unlockErr != nil {
				err = xerr.Join(err, fmt.Errorf("failed to unlock release: %w", unlockErr))
			}
		}()
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

	if _, _, err := commander.k8s.PruneReleaseDiff(ctx, previous, next, params.PruneOpts); err != nil {
		return fmt.Errorf("failed to prune release diff: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "successful descent of %s from revision %d to %d\n", params.Release, previousID, params.RevisionID)

	return nil
}

type PruneOpts = k8s.PruneOpts

type MaydayParams struct {
	Release   string
	Namespace string
	PruneOpts
}

func (commander Commander) Mayday(ctx context.Context, params MaydayParams) error {
	defer internal.DebugTimer(ctx, "mayday")()

	targetNS := cmp.Or(params.Namespace, commander.k8s.DefaultNamespace)

	release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release: %w", err)
	}

	if len(release.History) == 0 {
		return internal.Warning(fmt.Sprintf("mayday noop: no history found for release %q in namespace %q", params.Release, targetNS))
	}

	stages, err := commander.k8s.GetRevisionResources(ctx, release.ActiveRevision())
	if err != nil {
		return fmt.Errorf("failed to get resources for current revision: %w", err)
	}

	if _, _, err := commander.k8s.PruneReleaseDiff(ctx, stages, nil, params.PruneOpts); err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "Removed %d resource(s)...\n\n", len(stages.Flatten()))

	if err := commander.k8s.DeleteRevisions(ctx, *release); err != nil {
		return fmt.Errorf("failed to delete revision history: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "Successfully deleted release %s\n", params.Release)

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

	targetNS := cmp.Or(params.Namespace, commander.k8s.DefaultNamespace)

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

	ignoredProps := [][]string{
		{"metadata", "generation"},
		{"metadata", "resourceVersion"},
		{"metadata", "managedFields"},
		{"metadata", "creationTimestamp"},
		{"status"},
	}

	actual := map[string]*unstructured.Unstructured{}
	for name, resource := range expected {
		expected[name] = internal.DropProperties(resource, ignoredProps)
		value, err := commander.k8s.GetInClusterState(ctx, resource)
		if err != nil {
			return fmt.Errorf("failed to get in cluster state for resource %s: %w", internal.Canonical(resource), err)
		}
		value = internal.DropProperties(value, ignoredProps)
		if value != nil && params.ConflictsOnly {
			value.Object = internal.RemoveAdditions(resource.Object, value.Object)
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

type UnlockParams struct {
	Release   string
	Namespace string
}

func (commander Commander) UnlockRelease(ctx context.Context, params UnlockParams) error {
	return commander.k8s.UnlockRelease(ctx, internal.Release{
		Name:      params.Release,
		Namespace: cmp.Or(params.Namespace, commander.k8s.DefaultNamespace),
	})
}
