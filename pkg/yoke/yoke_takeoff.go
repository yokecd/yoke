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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/davidmdm/x/xerr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/text"
	"github.com/yokecd/yoke/internal/wasi"
)

type FlightParams struct {
	Path                string
	Module              *wasi.Module
	Input               io.Reader
	Args                []string
	Namespace           string
	CompilationCacheDir string
}

type TakeoffParams struct {
	// Directly send the result of evaluating the flight to stdout instead of applying it.
	SendToStdout bool

	// Skips running apply in dry-run before applying resources. Not recommended unless you know what you are doing and have specific reason to do so.
	// May be removed in a future release.
	SkipDryRun bool

	// DryRun will send the patch apply requests to the k8s api server in dry-run mode only.
	// Will not apply any state changes, nor create a revision to the release history.
	DryRun bool

	// ForceConflicts applies the path request with force. It will take ownership of fields owned by other fieldManagers.
	ForceConflicts bool

	// CrossNamespace allows for a release to create resources in a namespace other than the release's own namespace.
	CrossNamespace bool

	// Name of release
	Release string

	// Out is a folder to which to write all the resources hierarchically. This does not create a release or apply any state to the cluster.
	// This is useful for debugging/inspecting output or for working CDK8s style using kubectl apply --recursive.
	Out string

	// Parameters for the flight.
	Flight FlightParams

	// Do not apply the release but diff it against the current active version.
	DiffOnly bool

	// How many lines of context in the diff. Has no effect if DiffOnly is false.
	Context int

	// Output diffs with ansi colors.
	Color bool

	// Create namespace of target release if not exists.
	CreateNamespace bool

	// Wait interval for resources to become ready after being applied. The same wait interval is used as a timeout for each stage in a release.
	// Therefore if a stage contains a long-running job or workload it is important to set the wait time to a sufficiently long duration.
	Wait time.Duration

	// Poll interval to check for resource readiness.
	Poll time.Duration

	// OwnerReferences to be added to each resource found in release.
	OwnerReferences []metav1.OwnerReference

	// ClusterAccess grants the flight access to the kubernetes cluster. Users will be able to use the host k8s_lookup function.
	ClusterAccess bool
}

func (commander Commander) Takeoff(ctx context.Context, params TakeoffParams) error {
	defer internal.DebugTimer(ctx, "takeoff of "+params.Release)()

	output, wasm, err := EvalFlight(
		ctx,
		func() *k8s.Client {
			if !params.ClusterAccess {
				return nil
			}
			return commander.k8s
		}(),
		params.Release,
		params.Flight,
	)
	if err != nil {
		return fmt.Errorf("failed to evaluate flight: %w", err)
	}

	if params.SendToStdout {
		_, err = internal.Stdout(ctx).Write(output)
		return err
	}

	var stages internal.Stages
	if err := kyaml.Unmarshal(output, &stages); err != nil {
		return fmt.Errorf("failed to unmarshal raw resources: %w", err)
	}

	targetNS := cmp.Or(params.Flight.Namespace, "default")
	for _, stage := range stages {
		internal.AddYokeMetadata(stage, params.Release, targetNS)
	}

	if err := func() error {
		defer internal.DebugTimer(ctx, "looking up resource mappings")()

		var errs []error
		for _, stage := range stages {
			for _, resource := range stage {
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
					} else if !params.CrossNamespace && ns != targetNS {
						errs = append(errs, fmt.Errorf("%s: namespace %q does not match target namespace %q", internal.Canonical(resource), ns, targetNS))
					}
				}
				resource.SetOwnerReferences(params.OwnerReferences)
			}
		}

		return xerr.MultiErrFrom("Multiple namespaces detected (if desired enable multinamespace releases)", errs...)
	}(); err != nil {
		return err
	}

	if params.Out != "" {
		if params.Out == "-" {
			return ExportToStdout(ctx, stages.Flatten())
		}
		return ExportToFS(params.Out, params.Release, stages.Flatten())
	}

	if params.DiffOnly {
		release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
		if err != nil {
			return fmt.Errorf("failed to get revision history: %w", err)
		}
		current, err := commander.k8s.GetRevisionResources(ctx, release.ActiveRevision())
		if err != nil {
			return fmt.Errorf("failed to get current resources for revision: %w", err)
		}

		a, err := text.ToYamlFile("current", internal.CanonicalObjectMap(current.Flatten()))
		if err != nil {
			return err
		}

		b, err := text.ToYamlFile("next", internal.CanonicalObjectMap(stages.Flatten()))
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

	release, err := commander.k8s.GetRelease(ctx, params.Release, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release %q: %w", params.Release, err)
	}

	previous, err := func() (internal.Stages, error) {
		if len(release.History) == 0 {
			return nil, nil
		}
		return commander.k8s.GetRevisionResources(ctx, release.ActiveRevision())
	}()
	if err != nil {
		return fmt.Errorf("failed to get previous resources for revision: %w", err)
	}

	if reflect.DeepEqual(previous, stages) {
		return internal.Warning("resources are the same as previous revision: skipping takeoff")
	}

	if params.CreateNamespace {
		if err := commander.k8s.EnsureNamespace(ctx, targetNS); err != nil {
			return fmt.Errorf("failed to ensure namespace: %w", err)
		}
		if err := commander.k8s.WaitForReady(ctx, toUnstructuredNS(targetNS), k8s.WaitOptions{Interval: params.Poll}); err != nil {
			return fmt.Errorf("failed to wait for namespace %s to be ready: %w", targetNS, err)
		}
	}

	applyOpts := k8s.ApplyResourcesOpts{
		DryRunOnly:     params.DryRun,
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
	}

	for i, stage := range stages {
		if err := commander.k8s.ApplyResources(ctx, stage, applyOpts); err != nil {
			return fmt.Errorf("failed to apply resources: %w", err)
		}

		// If this is not the last or only stage we need a default wait/poll interval.
		// The entire point of stages is the ordering of interdependent resources.
		var waitOpts k8s.WaitOptions
		if i < len(stages)-1 {
			waitOpts.Timeout = cmp.Or(params.Wait, 30*time.Second)
			waitOpts.Interval = cmp.Or(params.Poll, 2*time.Second)
		}

		if params.Wait > 0 {
			if err := commander.k8s.WaitForReadyMany(ctx, stage, waitOpts); err != nil {
				return fmt.Errorf("release did not become ready within wait period: to rollback use `yoke descent`: %w", err)
			}
		}
	}

	if params.DryRun {
		fmt.Fprintf(internal.Stderr(ctx), "successful dry-run takeoff of %s\n", params.Release)
		return nil
	}

	now := time.Now()
	if err := commander.k8s.CreateRevision(
		ctx,
		params.Release,
		targetNS,
		internal.Revision{
			Source:    internal.SourceFrom(params.Flight.Path, wasm),
			CreatedAt: now,
			ActiveAt:  now,
			Resources: len(stages.Flatten()),
		},
		stages,
	); err != nil {
		return fmt.Errorf("failed to create revision: %w", err)
	}

	if _, err := commander.k8s.RemoveOrphans(ctx, previous, stages); err != nil {
		return fmt.Errorf("failed to remove orhpans: %w", err)
	}

	fmt.Fprintf(internal.Stderr(ctx), "successful takeoff of %s\n", params.Release)

	return nil
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
