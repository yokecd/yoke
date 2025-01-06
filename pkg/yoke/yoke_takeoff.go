package yoke

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tetratelabs/wazero"

	"github.com/davidmdm/x/xerr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/text"
)

type FlightParams struct {
	Path                string
	WasmModule          wazero.CompiledModule
	Input               io.Reader
	Args                []string
	Namespace           string
	CompilationCacheDir string
}

type TakeoffParams struct {
	SendToStdout     bool
	SkipDryRun       bool
	DryRun           bool
	ForceConflicts   bool
	MultiNamespaces  bool
	Release          string
	Out              string
	Flight           FlightParams
	DiffOnly         bool
	Context          int
	Color            bool
	CreateNamespaces bool
	CreateCRDs       bool
	Wait             time.Duration
	Poll             time.Duration
	OwnerReferences  []metav1.OwnerReference
}

func (commander Commander) Takeoff(ctx context.Context, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "takeoff of "+params.Release)()

	output, wasm, err := EvalFlight(ctx, params.Release, params.Flight)
	if err != nil {
		return fmt.Errorf("failed to evaluate flight: %w", err)
	}

	if params.SendToStdout {
		_, err = internal.Stdout(ctx).Write(output)
		return err
	}

	var resources internal.List[*unstructured.Unstructured]
	if err := kyaml.Unmarshal(output, &resources); err != nil {
		return fmt.Errorf("failed to unmarshal raw resources: %w", err)
	}

	dependencies, resources := SplitResources(resources)

	internal.AddYokeMetadata(dependencies.CRDs, params.Release)
	internal.AddYokeMetadata(dependencies.Namespaces, params.Release)
	internal.AddYokeMetadata(resources, params.Release)

	targetNS := cmp.Or(params.Flight.Namespace, "default")

	if err := func() error {
		defer internal.DebugTimer(ctx, "looking up resource mappings")()

		var errs []error
		for _, resource := range resources {
			mapping, err := commander.k8s.LookupResourceMapping(resource)
			if err != nil {
				if meta.IsNoMatchError(err) {
					continue
				}

				return fmt.Errorf("failed to lookup resource mapping for %s: %w", internal.Canonical(resource), err)
			}
			if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
				ns := resource.GetNamespace()
				if ns == "" {
					resource.SetNamespace(targetNS)
				}
				if !params.MultiNamespaces && ns != targetNS {
					errs = append(errs, fmt.Errorf("%s: namespace %q does not match target namespace %q", internal.Canonical(resource), ns, targetNS))
				}
			}
			resource.SetOwnerReferences(params.OwnerReferences)
		}

		return xerr.MultiErrFrom("Multiple namespaces detected (if desired enable multinamespace releases)", errs...)
	}(); err != nil {
		return err
	}

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
		currentResources, err := commander.k8s.GetRevisionResources(ctx, revisions.Active())
		if err != nil {
			return fmt.Errorf("failed to get current resources for revision: %w", err)
		}

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
		return fmt.Errorf("failed to get revision history for release %q: %w", params.Release, err)
	}

	previous, err := func() ([]*unstructured.Unstructured, error) {
		if len(revisions.History) == 0 {
			return nil, nil
		}
		return commander.k8s.GetRevisionResources(ctx, revisions.Active())
	}()
	if err != nil {
		return fmt.Errorf("failed to get previous resources for revision: %w", err)
	}

	if reflect.DeepEqual(previous, []*unstructured.Unstructured(resources)) {
		return internal.Warning("resources are the same as previous revision: skipping takeoff")
	}

	dependencies.Namespaces = append(dependencies.Namespaces, toUnstructuredNS(targetNS))

	if params.CreateCRDs || params.CreateNamespaces {
		if err := commander.applyDependencies(ctx, dependencies, params); err != nil {
			return fmt.Errorf("failed to apply flight dependencies: %w", err)
		}
	}

	applyOpts := k8s.ApplyResourcesOpts{
		DryRunOnly:     params.DryRun,
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
		Release:        params.Release,
	}

	if err := commander.k8s.ApplyResources(ctx, resources, applyOpts); err != nil {
		return fmt.Errorf("failed to apply resources: %w", err)
	}

	if params.DryRun {
		fmt.Fprintf(internal.Stderr(ctx), "successful dry-run takeoff of %s\n", params.Release)
		return nil
	}

	now := time.Now()
	if err := commander.k8s.CreateRevision(
		ctx,
		params.Release,
		internal.Revision{
			Source:    internal.SourceFrom(params.Flight.Path, wasm),
			CreatedAt: now,
			ActiveAt:  now,
			Resources: len(resources),
		},
		resources,
	); err != nil {
		return fmt.Errorf("failed to create revision: %w", err)
	}

	if _, err := commander.k8s.RemoveOrphans(ctx, previous, resources); err != nil {
		return fmt.Errorf("failed to remove orhpans: %w", err)
	}

	if params.Wait > 0 {
		if err := commander.k8s.WaitForReadyMany(ctx, resources, k8s.WaitOptions{Timeout: params.Wait, Interval: params.Poll}); err != nil {
			return fmt.Errorf("release did not become ready within wait period: to rollback use `yoke descent`: %w", err)
		}
	}

	fmt.Fprintf(internal.Stderr(ctx), "successful takeoff of %s\n", params.Release)

	return nil
}

func (commander Commander) applyDependencies(ctx context.Context, dependencies FlightDependencies, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "apply-dependencies")()

	wg := sync.WaitGroup{}
	errs := make([]error, 2)

	applyOpts := k8s.ApplyResourcesOpts{
		DryRunOnly:     params.DryRun,
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
		Release:        params.Release,
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

	var errs []error
	for _, resource := range resources {
		path := filepath.Join(root, internal.Canonical(resource)+".yaml")

		parent, _ := filepath.Split(path)

		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("failed to create release output directory: %w", err)
		}

		if err := internal.WriteYAML(path, resource.Object); err != nil {
			errs = append(errs, err)
		}
	}

	return xerr.MultiErrFrom("failed to write resource(s)", errs...)
}

func ExportToStdout(ctx context.Context, resources []*unstructured.Unstructured) error {
	output := make(map[string]any, len(resources))
	for _, resource := range resources {
		segments := strings.Split(internal.Canonical(resource), "/")
		obj := output
		for i, segment := range segments {
			if i == len(segments)-1 {
				obj[segment] = resource.Object
				break
			}
			if _, ok := obj[segment]; !ok {
				obj[segment] = map[string]any{}
			}
			obj = obj[segment].(map[string]any)
		}
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
