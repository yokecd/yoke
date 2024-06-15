package yoke

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/k8s"
	"github.com/davidmdm/yoke/internal/text"
)

type FlightParams struct {
	Path      string
	Input     io.Reader
	Args      []string
	Namespace string
}

type TakeoffParams struct {
	TestRun          bool
	SkipDryRun       bool
	ForceConflicts   bool
	Release          string
	Out              string
	Flight           FlightParams
	DiffOnly         bool
	Context          int
	Color            bool
	CreateNamespaces bool
	CreateCRDs       bool
	Wait             time.Duration
}

func (commander Commander) Takeoff(ctx context.Context, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "takeoff")()

	output, wasm, err := EvalFlight(ctx, params.Release, params.Flight)
	if err != nil {
		return fmt.Errorf("failed to evaluate flight: %w", err)
	}
	if params.TestRun {
		_, err = fmt.Print(string(output))
		return err
	}

	var resources internal.List[*unstructured.Unstructured]
	if err := kyaml.Unmarshal(output, &resources); err != nil {
		return fmt.Errorf("failed to unmarshal raw resources: %w", err)
	}

	dependencies, resources := SplitResources(resources)

	if params.CreateCRDs || params.CreateNamespaces {
		if err := commander.applyDependencies(ctx, dependencies, params); err != nil {
			return fmt.Errorf("failed to apply flight dependencies: %w", err)
		}
	}

	complete := internal.DebugTimer(ctx, "looking up resource mappings")

	for _, resource := range resources {
		mapping, err := commander.k8s.LookupResourceMapping(resource)
		if err != nil {
			if meta.IsNoMatchError(err) {
				continue
			}
			return fmt.Errorf("failed to lookup resource mapping for %s: %w", internal.Canonical(resource), err)
		}
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace && resource.GetNamespace() == "" {
			resource.SetNamespace(cmp.Or(params.Flight.Namespace, "default"))
		}
	}

	complete()

	internal.AddYokeMetadata(resources, params.Release)

	if params.Out != "" {
		if params.Out == "-" {
			return ExportToStdout(ctx, resources)
		}
		return ExportToFS(params.Out, params.Release, resources)
	}

	if params.DiffOnly {
		revisions, err := commander.k8s.GetRevisions(ctx, params.Release)
		if err != nil {
			return fmt.Errorf("failed to get revision history: %w", err)
		}
		currentResources := revisions.CurrentResources()

		a, err := text.ToYamlFile("current", internal.CanonicalObjectMap(currentResources))
		if err != nil {
			return err
		}

		b, err := text.ToYamlFile("next", internal.CanonicalObjectMap(resources))
		if err != nil {
			return err
		}

		differ := func() text.DiffFunc {
			if params.Color {
				return text.DiffColorized
			}
			return text.Diff
		}()

		_, err = fmt.Fprint(internal.Stdout(ctx), differ(a, b, params.Context))
		return err
	}

	revisions, err := commander.k8s.GetRevisions(ctx, params.Release)
	if err != nil {
		return fmt.Errorf("failed to get revision history: %w", err)
	}

	previous := revisions.CurrentResources()

	if reflect.DeepEqual(previous, []*unstructured.Unstructured(resources)) {
		return internal.Warning("resources are the same as previous revision: skipping takeoff")
	}

	if err := commander.k8s.ValidateOwnership(ctx, params.Release, resources); err != nil {
		return fmt.Errorf("failed to validate ownership: %w", err)
	}

	if namespace := params.Flight.Namespace; namespace != "" {
		if err := commander.k8s.EnsureNamespace(ctx, namespace); err != nil {
			return fmt.Errorf("failed to ensure namespace: %w", err)
		}
		if err := commander.k8s.WaitForReady(ctx, toUnstructuredNS(namespace), k8s.WaitOptions{}); err != nil {
			return fmt.Errorf("failed to wait for namespace %s to be ready: %w", namespace, err)
		}
	}

	applyOpts := k8s.ApplyResourcesOpts{
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
	}

	if err := commander.k8s.ApplyResources(ctx, resources, applyOpts); err != nil {
		return fmt.Errorf("failed to apply resources: %w", err)
	}

	revisions.Add(resources, params.Flight.Path, wasm)

	if err := commander.k8s.UpsertRevisions(ctx, params.Release, revisions); err != nil {
		return fmt.Errorf("failed to create revision: %w", err)
	}

	removed, err := commander.k8s.RemoveOrphans(ctx, previous, resources)
	if err != nil {
		return fmt.Errorf("failed to remove orhpans: %w", err)
	}

	var (
		createdNames = internal.CanonicalNameList(resources)
		removedNames = internal.CanonicalNameList(removed)
	)

	if err := commander.k8s.UpdateResourceReleaseMapping(ctx, params.Release, createdNames, removedNames); err != nil {
		return fmt.Errorf("failed to update resource release mapping: %w", err)
	}

	if params.Wait > 0 {
		if err := commander.k8s.WaitForReadyMany(ctx, resources, k8s.WaitOptions{Timeout: params.Wait}); err != nil {
			return fmt.Errorf("release did not become ready within wait period: to rollback use `yoke descent`: %w", err)
		}
	}

	return nil
}

func (commander Commander) applyDependencies(ctx context.Context, dependencies FlightDependencies, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "apply-dependencies")()

	wg := sync.WaitGroup{}
	errs := make([]error, 2)

	applyOpts := k8s.ApplyResourcesOpts{
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
	}

	if params.CreateCRDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := commander.k8s.ApplyResources(ctx, dependencies.CRDs, applyOpts); err != nil {
				errs[0] = fmt.Errorf("failed to create CRDs: %w", err)
				return
			}
			if err := commander.k8s.WaitForReadyMany(ctx, dependencies.CRDs, k8s.WaitOptions{}); err != nil {
				errs[0] = fmt.Errorf("failed to wait for CRDs to become ready: %w", err)
				return
			}
		}()
	}

	if params.CreateNamespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := commander.k8s.ApplyResources(ctx, dependencies.Namespaces, applyOpts); err != nil {
				errs[1] = fmt.Errorf("failed to create namespaces: %w", err)
				return
			}
			if err := commander.k8s.WaitForReadyMany(ctx, dependencies.Namespaces, k8s.WaitOptions{}); err != nil {
				errs[1] = fmt.Errorf("failed to wait for namespaces to become ready: %w", err)
				return
			}
		}()
	}

	wg.Wait()

	return xerr.MultiErrOrderedFrom("", errs...)
}

type FlightDependencies struct {
	Namespaces []*unstructured.Unstructured
	CRDs       []*unstructured.Unstructured
}

func SplitResources(resources []*unstructured.Unstructured) (deps FlightDependencies, core []*unstructured.Unstructured) {
	for _, resource := range resources {
		gvk := resource.GroupVersionKind()
		switch {
		case gvk.Kind == "Namespace" && gvk.Group == "":
			deps.Namespaces = append(deps.Namespaces, resource)
		case gvk.Kind == "CustomResourceDefinition" && gvk.Group == "apiextensions.k8s.io":
			deps.CRDs = append(deps.CRDs, resource)
		default:
			core = append(core, resource)
		}
	}
	return
}

func ExportToFS(dir, release string, resources []*unstructured.Unstructured) error {
	root := filepath.Join(dir, release)

	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("failed remove previous flight export: %w", err)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("failed to create release output directory: %w", err)
	}

	var errs []error
	for _, resource := range resources {
		path := filepath.Join(root, internal.Canonical(resource)+".yaml")

		if err := internal.WriteYAML(path, resource.Object); err != nil {
			errs = append(errs, err)
		}
	}

	return xerr.MultiErrFrom("failed to write resource(s)", errs...)
}

func ExportToStdout(ctx context.Context, resources []*unstructured.Unstructured) error {
	output := make(map[string]any, len(resources))
	for _, resource := range resources {
		output[internal.Canonical(resource)] = resource.Object
	}

	encoder := yaml.NewEncoder(internal.Stdout(ctx))
	encoder.SetIndent(2)
	return encoder.Encode(output)
}

func toUnstructuredNS(ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]any{"name": ns},
		},
	}
}
