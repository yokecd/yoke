package yoke

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/k8s"
	"github.com/davidmdm/yoke/internal/text"
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
	Wait       time.Duration
	Poll       time.Duration
}

func (commander Commander) Descent(ctx context.Context, params DescentParams) error {
	defer internal.DebugTimer(ctx, "descent")()

	revisions, err := commander.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revisions: %w", err)
	}

	if id := params.RevisionID; id < 1 || id > len(revisions.History) {
		return fmt.Errorf("requested revision id %d is not within valid range 1 to %d", id, len(revisions.History))
	}

	targetRevision := revisions.History[params.RevisionID-1]

	next, err := commander.k8s.GetRevisionResources(ctx, targetRevision)
	if err != nil {
		return fmt.Errorf("failed to lookup target revision resources: %w", err)
	}

	if err := commander.k8s.ValidateOwnership(ctx, params.Release, next); err != nil {
		return fmt.Errorf("failed to validate ownership: %w", err)
	}

	previous, err := commander.k8s.GetRevisionResources(ctx, revisions.Active())
	if err != nil {
		return fmt.Errorf("failed to lookup current revision resources: %w", err)
	}

	if err := commander.k8s.ApplyResources(ctx, next, k8s.ApplyResourcesOpts{SkipDryRun: true}); err != nil {
		return fmt.Errorf("failed to apply resources: %w", err)
	}

	if err := commander.k8s.UpdateRevisionActiveState(ctx, targetRevision.Name); err != nil {
		return fmt.Errorf("failed to update revision history: %w", err)
	}

	removed, err := commander.k8s.RemoveOrphans(ctx, previous, next)
	if err != nil {
		return fmt.Errorf("failed to remove orphaned resources: %w", err)
	}

	var (
		createdNames = internal.CanonicalNameList(next)
		removedNames = internal.CanonicalNameList(removed)
	)

	if err := commander.k8s.UpdateResourceReleaseMapping(ctx, params.Release, createdNames, removedNames); err != nil {
		return fmt.Errorf("failed to update resource release mapping: %w", err)
	}

	if params.Wait > 0 {
		if err := commander.k8s.WaitForReadyMany(ctx, next, k8s.WaitOptions{Timeout: params.Wait, Interval: params.Poll}); err != nil {
			return fmt.Errorf("release did not become ready within wait period: %w", err)
		}
	}

	return nil
}

func (client Commander) Mayday(ctx context.Context, release string) error {
	defer internal.DebugTimer(ctx, "mayday")()

	revisions, err := client.k8s.GetRevisions(ctx, release)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release: %w", err)
	}

	resources, err := client.k8s.GetRevisionResources(ctx, revisions.Active())
	if err != nil {
		return fmt.Errorf("failed to get resources for current revision: %w", err)
	}

	removed, err := client.k8s.RemoveOrphans(ctx, resources, nil)
	if err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	if err := client.k8s.UpdateResourceReleaseMapping(ctx, release, nil, internal.CanonicalNameList(removed)); err != nil {
		return fmt.Errorf("failed to update resource to release mapping: %w", err)
	}

	if err := client.k8s.DeleteRevisions(ctx, *revisions); err != nil {
		return fmt.Errorf("failed to delete revision history: %w", err)
	}

	return nil
}

type TurbulenceParams struct {
	Release       string
	Context       int
	ConflictsOnly bool
	Fix           bool
	Color         bool
}

func (commander Commander) Turbulence(ctx context.Context, params TurbulenceParams) error {
	defer internal.DebugTimer(ctx, "turbulence")()

	revisions, err := commander.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revisions for release %s: %w", params.Release, err)
	}

	resources, err := commander.k8s.GetRevisionResources(ctx, revisions.Active())
	if err != nil {
		return fmt.Errorf("failed to get current resources: %w", err)
	}

	expected := internal.CanonicalMap(resources)

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
