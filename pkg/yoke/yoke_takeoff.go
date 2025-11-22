package yoke

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/davidmdm/x/xerr"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/text"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
)

type ModuleSourcetadata = internal.Source

type Module struct {
	Instance       *wasi.Module
	SourceMetadata ModuleSourcetadata
}

type FlightParams struct {
	Path                string
	Insecure            bool
	Module              Module
	Args                []string
	CompilationCacheDir string

	// MaxMemoryMib is the maximum amount of memory a flight can allocate. If this is not set, the flight can use the maximum amount of memory available.
	// The maximum memory abailable is 4gb or 4096mb
	MaxMemoryMib uint64

	// Timeout sets a custom timeout flight run time. If exceeded, execution will fail with a Deadline Exceeded error.
	// By default flights have a max runtime of 10 seconds. Setting this to a negative duration removes all timeouts.
	// Running without timeouts is not recommended but you do you.
	Timeout time.Duration

	// Env specifies user-defined envvars to be added to the flight execution.
	// Standard yoke envvars will take precendence.
	Env map[string]string

	// Stderr is the writer that will be exposed to the wasm module as os.Stderr.
	// If not provided all stderr writes in the wasm module will be buffered instead
	// and surfaced to the user only on error exit codes.
	Stderr io.Writer
	Input  io.Reader
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

	// ForceOwnership allows yoke releases to take over ownership of previously existing resources.
	ForceOwnership bool

	// CrossNamespace allows for a release to create resources in a namespace other than the release's own namespace.
	CrossNamespace bool

	// Name of release
	Release string

	// ReleasePrefix prefixes the release name. The full name of the release will be releasePrefix+release.
	// However the YOKE_RELEASE envvar will only be release. This allows us users to set release names that can be used in the Flight
	// but dedup them with a prefix like in the case of the ATC Flight and ClusterFlight CRs.
	ReleasePrefix string

	// Release Namespace
	Namespace string

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
	// This includes enabling/disabling cluster-access and granting any external resource matchers.
	ClusterAccess ClusterAccessParams

	// HistoryCapSize limits the number of revisions kept in the release's history by the size. If Cap is less than 1 history is uncapped.
	HistoryCapSize int

	// IdentityFunc is provided in contexts such as the AirTrafficController where the flight may return resources that reference itself.
	// If the IdentityFunc is passed any resources that match the predicate should be removed from the stages.
	IdentityFunc func(*unstructured.Unstructured) bool

	// ExtraLabels adds extra labels to resources deployed by yoke.
	// Example: used by the ATC to add Instance metadata
	ExtraLabels map[string]string

	// ManagedBy is the value used in the yoke managed-by label. If left empty will default to `yoke`.
	ManagedBy string

	// PruneOpts controls if namespaces and crds are removed when creating new releases.
	// If a prior release had a namespace or crd resource and the next revision of the release would not have it
	// setting prune options would allow you to remove the resources. By default CRDs and Namespaces are not pruned.
	PruneOpts

	// Lock defines whether the lock should not be taken during takeoff. By default the lock is not taken and conflicts can arise.
	// Processes that opt-in to using the locking mechanism cannot run at the same time.
	// This feature is opt-in since locking can cause conflicts with release namespaces that would be created by the namespace.
	// This is an unfortunate reality of supporting kubectl apply yaml dumps.
	Lock bool
}

func (commander Commander) Takeoff(ctx context.Context, params TakeoffParams) (err error) {
	defer internal.DebugTimer(ctx, "takeoff of "+params.Release)()

	output, wasm, err := EvalFlight(
		ctx,
		EvalParams{
			Client:        commander.k8s,
			Release:       params.Release,
			Namespace:     params.Namespace,
			ClusterAccess: params.ClusterAccess,
			Flight:        params.Flight,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to evaluate flight: %w", err)
	}

	if len(output) == 0 {
		return fmt.Errorf("failed to takeoff: resource provided is either empty or invalid")
	}

	if params.SendToStdout {
		_, err = internal.Stdout(ctx).Write(output)
		return err
	}

	stages, err := internal.ParseStages(output)
	if err != nil {
		return fmt.Errorf("failed to parse output into valid flight output: %w", err)
	}

	if params.IdentityFunc != nil {
		for i, stage := range stages {
			stages[i] = func() internal.Stage {
				result := make(internal.Stage, 0, len(stage))
				for _, resource := range stage {
					if !params.IdentityFunc(resource) {
						result = append(result, resource)
					}
				}
				return result
			}()
		}
	}

	if params.Out != "" {
		if params.Out == "-" {
			return ExportToStdout(ctx, stages.Flatten())
		}
		return ExportToFS(params.Out, params.Release, stages.Flatten())
	}

	targetNS := cmp.Or(params.Namespace, "default")
	for _, stage := range stages {
		internal.AddYokeMetadata(stage, params.Release, targetNS, params.ManagedBy)
	}

	if !params.CrossNamespace {
		if err := func() error {
			var errs []error
			for _, resource := range stages.Flatten() {
				if ns := resource.GetNamespace(); ns != "" && ns != targetNS {
					errs = append(errs, fmt.Errorf("%s: namespace %q does not match target namespace %q", internal.Canonical(resource), ns, targetNS))
				}
			}
			return xerr.MultiErrFrom("Multiple namespaces detected (if desired enable multinamespace releases)", errs...)
		}(); err != nil {
			return err
		}
	}

	if len(params.OwnerReferences) > 0 {
		for _, resource := range stages.Flatten() {
			resource.SetOwnerReferences(append(resource.GetOwnerReferences(), params.OwnerReferences...))
		}
	}

	if len(params.ExtraLabels) > 0 {
		for _, resource := range stages.Flatten() {
			labels := resource.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			maps.Copy(labels, params.ExtraLabels)
			resource.SetLabels(labels)
		}
	}

	if err := setTargetNS(commander.k8s, stages.Flatten(), targetNS); err != nil {
		return err
	}

	dropUndesiredMetaProps(stages.Flatten())

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

	if params.CreateNamespace {
		if err := commander.k8s.EnsureNamespace(ctx, targetNS); err != nil {
			return fmt.Errorf("failed to ensure namespace: %w", err)
		}
		if err := commander.k8s.WaitForReady(ctx, toUnstructuredNS(targetNS), k8s.WaitOptions{Interval: params.Poll}); err != nil {
			return fmt.Errorf("failed to wait for namespace %s to be ready: %w", targetNS, err)
		}
	}

	fullReleaseName := params.ReleasePrefix + params.Release

	release, err := commander.k8s.GetRelease(ctx, fullReleaseName, targetNS)
	if err != nil {
		return fmt.Errorf("failed to get revision history for release %q: %w", params.Release, err)
	}

	if !params.DryRun && params.Lock {
		if err := commander.k8s.LockRelease(ctx, *release); err != nil {
			return fmt.Errorf("failed to lock release: %w", err)
		}
		defer func() {
			if unlockErr := commander.k8s.UnlockRelease(ctx, *release); unlockErr != nil {
				err = xerr.Join(err, fmt.Errorf("failed to unlock release: %w", unlockErr))
			}
		}()
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

	applyOpts := k8s.ApplyResourcesOpts{
		ApplyOpts: k8s.ApplyOpts{
			DryRun:         params.DryRun,
			ForceConflicts: params.ForceConflicts,
			ForceOwnership: params.ForceOwnership,
		},
		SkipDryRun: params.SkipDryRun,
	}

	for i, stage := range stages {
		if err := commander.k8s.ApplyResources(ctx, stage, applyOpts); err != nil {
			return fmt.Errorf("failed to apply resources: %w", err)
		}

		if params.DryRun {
			// If we are running in dry-run mode, we are not actually applying resources
			// so we do not want to wait for them to become ready.
			continue
		}

		waitOpts := k8s.WaitOptions{
			Timeout:  params.Wait,
			Interval: params.Poll,
		}

		// If this is not the last or only stage we need a default wait/poll interval.
		// The entire point of stages is the ordering of interdependent resources.
		if i < len(stages)-1 {
			waitOpts.Timeout = cmp.Or(params.Wait, 30*time.Second)
			waitOpts.Interval = cmp.Or(params.Poll, 2*time.Second)
		}

		if waitOpts.Timeout > 0 {
			if err := commander.k8s.WaitForReadyMany(ctx, stage, waitOpts); err != nil {
				return fmt.Errorf("release did not become ready within wait period: to rollback use `yoke descent`: %w", err)
			}
		}
	}

	if params.DryRun {
		fmt.Fprintf(internal.Stderr(ctx), "successful dry-run takeoff of %s\n", params.Release)
		return nil
	}

	if reflect.DeepEqual(previous, stages) {
		return internal.Warning("resources are the same as previous revision: skipping creation of new revision")
	}

	now := time.Now()
	if err := commander.k8s.CreateRevision(
		ctx,
		fullReleaseName,
		targetNS,
		internal.Revision{
			Source: func() internal.Source {
				if params.Flight.Path == "" {
					return params.Flight.Module.SourceMetadata
				}
				return internal.SourceFrom(params.Flight.Path, wasm)
			}(),
			CreatedAt: now,
			ActiveAt:  now,
			Resources: len(stages.Flatten()),
		},
		stages,
	); err != nil {
		return fmt.Errorf("failed to create revision: %w", err)
	}

	if _, _, err := commander.k8s.PruneReleaseDiff(ctx, previous, stages, params.PruneOpts); err != nil {
		return fmt.Errorf("failed to prune release diff: %w", err)
	}

	if params.HistoryCapSize > 0 {
		if err := commander.k8s.CapReleaseHistory(ctx, fullReleaseName, targetNS, params.HistoryCapSize); err != nil {
			return internal.Warning(fmt.Sprintf("failed to cap release history after successful takeoff of %s: %v", params.Release, err))
		}
	}

	// If the context of ReleaseTracking from the ATC, we may want to capture the resources that were deployed.
	// This is only used in SubscriptionMode for airways. Otherwise has no effect.
	host.SetReleaseResources(ctx, stages.Flatten())

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

// dropUndesiredMetaProps removes properties that may be provided to yoke via some resource lookup
// but that do not belong in a server-side apply. These include resource version, managedFields, creationtimestamp,
// and possibly more.
func dropUndesiredMetaProps(resources []*unstructured.Unstructured) {
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		unstructured.RemoveNestedField(resource.Object, "metadata", "resourceVersion")
		unstructured.RemoveNestedField(resource.Object, "metadata", "creationTimestamp")
		unstructured.RemoveNestedField(resource.Object, "metadata", "uid")
		unstructured.RemoveNestedField(resource.Object, "metadata", "managedFields")
	}
}

func setTargetNS(client *k8s.Client, resources []*unstructured.Unstructured, ns string) error {
	// Using bool instead of struct{} to be able to use it conveniantly in an if statement below.
	// Memory be damned!
	crdScopes := map[schema.GroupKind]bool{}

	for _, resource := range resources {
		if resource.GetKind() != "CustomResourceDefinition" || resource.GetAPIVersion() != "apiextensions.k8s.io/v1" {
			continue
		}
		var crd apiextv1.CustomResourceDefinition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, &crd); err != nil {
			return fmt.Errorf("%s: failed to convert unstructured resource to CRD: %w", internal.Canonical(resource), err)
		}

		crdScopes[schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}] = crd.Spec.Scope == apiextv1.NamespaceScoped
	}

	var errs []error
	for _, resource := range resources {
		// We need to check if the resource is a custom resource whose definition is in the flight.
		// Otherwise we will fail to lookup the mapping.
		if namespacedScoped, ok := crdScopes[resource.GroupVersionKind().GroupKind()]; ok {
			if resource.GetNamespace() == "" && namespacedScoped {
				resource.SetNamespace(ns)
			}
			continue
		}
		mapping, err := client.LookupResourceMapping(resource)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: failed to lookup resource mapping: %w", internal.Canonical(resource), err))
			continue
		}
		if mapping.Scope.Name() != meta.RESTScopeNameNamespace || resource.GetNamespace() != "" {
			continue
		}
		resource.SetNamespace(ns)
	}

	return xerr.MultiErrFrom("setting target namespace", errs...)
}
