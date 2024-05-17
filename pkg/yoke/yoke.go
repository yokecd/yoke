package yoke

import (
	"context"
	"fmt"
	"os"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/k8s"
	"github.com/davidmdm/yoke/internal/text"
)

type Client struct {
	k8s *k8s.Client
}

func FromKubeConfig(path string) (*Client, error) {
	client, err := k8s.NewClientFromKubeConfig(path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize k8s client: %w", err)
	}
	return &Client{client}, nil
}

func FromK8Client(client *k8s.Client) *Client {
	return &Client{client}
}

type TakeoffParams struct {
	Release        string
	Resources      []*unstructured.Unstructured
	FlightID       string
	Namespace      string
	Wasm           []byte
	SkipDryRun     bool
	ForceConflicts bool
}

func (client Client) Takeoff(ctx context.Context, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "takeoff")()

	revisions, err := client.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revision history: %w", err)
	}

	previous := revisions.CurrentResources()

	if reflect.DeepEqual(previous, params.Resources) {
		return internal.Warning("resources are the same as previous revision: skipping takeoff")
	}

	if err := client.k8s.ValidateOwnership(ctx, params.Release, params.Resources); err != nil {
		return fmt.Errorf("failed to validate ownership: %w", err)
	}

	if params.Namespace != "" {
		if err := client.k8s.EnsureNamespace(ctx, params.Namespace); err != nil {
			return fmt.Errorf("failed to ensure namespace: %w", err)
		}
	}

	applyOpts := k8s.ApplyResourcesOpts{
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
	}
	if err := client.k8s.ApplyResources(ctx, params.Resources, applyOpts); err != nil {
		return fmt.Errorf("failed to apply resources: %w", err)
	}

	revisions.Add(params.Resources, params.FlightID, params.Wasm)

	if err := client.k8s.UpsertRevisions(ctx, params.Release, revisions); err != nil {
		return fmt.Errorf("failed to create revision: %w", err)
	}

	removed, err := client.k8s.RemoveOrphans(ctx, previous, params.Resources)
	if err != nil {
		return fmt.Errorf("failed to remove orhpans: %w", err)
	}

	var (
		createdNames = internal.CanonicalNameList(params.Resources)
		removedNames = internal.CanonicalNameList(removed)
	)

	if err := client.k8s.UpdateResourceReleaseMapping(ctx, params.Release, createdNames, removedNames); err != nil {
		return fmt.Errorf("failed to update resource release mapping: %w", err)
	}

	return nil
}

type DescentParams struct {
	Release    string
	RevisionID int
}

func (client Client) Descent(ctx context.Context, params DescentParams) error {
	revisions, err := client.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revisions: %w", err)
	}

	index, next := func() (int, *internal.Revision) {
		for i, revision := range revisions.History {
			if revision.ID == params.RevisionID {
				return i, &revision
			}
		}
		return 0, nil
	}()

	if next == nil {
		return fmt.Errorf("revision %d is not within history", params.RevisionID)
	}

	if err := client.k8s.ValidateOwnership(ctx, params.Release, next.Resources); err != nil {
		return fmt.Errorf("failed to validate ownership: %w", err)
	}

	previous := revisions.CurrentResources()

	if err := client.k8s.ApplyResources(ctx, next.Resources, k8s.ApplyResourcesOpts{SkipDryRun: true}); err != nil {
		return fmt.Errorf("failed to apply resources: %w", err)
	}

	revisions.ActiveIndex = index

	if err := client.k8s.UpsertRevisions(ctx, params.Release, revisions); err != nil {
		return fmt.Errorf("failed to update revision history: %w", err)
	}

	removed, err := client.k8s.RemoveOrphans(ctx, previous, next.Resources)
	if err != nil {
		return fmt.Errorf("failed to remove orphaned resources: %w", err)
	}

	var (
		createdNames = internal.CanonicalNameList(next.Resources)
		removedNames = internal.CanonicalNameList(removed)
	)

	if err := client.k8s.UpdateResourceReleaseMapping(ctx, params.Release, createdNames, removedNames); err != nil {
		return fmt.Errorf("failed to update resource release mapping: %w", err)
	}

	return nil
}

func (client Client) Mayday(ctx context.Context, release string) error {
	revisions, err := client.k8s.GetRevisions(ctx, release)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release: %w", err)
	}

	removed, err := client.k8s.RemoveOrphans(ctx, revisions.CurrentResources(), nil)
	if err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	if err := client.k8s.UpdateResourceReleaseMapping(ctx, release, nil, internal.CanonicalNameList(removed)); err != nil {
		return fmt.Errorf("failed to update resource to release mapping: %w", err)
	}

	if err := client.k8s.DeleteRevisions(ctx, release); err != nil {
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

func (client Client) Turbulence(ctx context.Context, params TurbulenceParams) error {
	revisions, err := client.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revisions for release %s: %w", params.Release, err)
	}
	resources := revisions.CurrentResources()

	expected := make(map[string]*unstructured.Unstructured, len(resources))
	for _, resource := range resources {
		expected[internal.Canonical(resource)] = resource
	}

	actual := map[string]*unstructured.Unstructured{}
	for name, resource := range expected {
		value, err := client.k8s.GetInClusterState(ctx, resource)
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
			if err := client.k8s.ApplyResource(ctx, desired, forceConflicts); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
			fmt.Fprintf(os.Stderr, "fixed drift for: %s\n", name)
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

	_, err = fmt.Fprint(os.Stdout, diff)
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
