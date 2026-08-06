package atc

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/x/xsync"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/k8s/ctrl"
	"github.com/yokecd/yoke/pkg/yoke"
)

type InstanceReconcilerParams struct {
	GK      schema.GroupKind
	Version string
	Airway  v1alpha1.Airway
	States  *xsync.Map[string, InstanceState]
}

func (atc atc) InstanceReconciler(params InstanceReconcilerParams) ctrl.Funcs {
	reconciler := func(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
		defer func() {
			if cache.IsDisallowedModuleError(err) {
				err = ctrl.Terminal(err)
			}
		}()

		client := (*k8s.Client)(ctrl.Client(ctx))
		resourceCache := ctrl.CacheFromEvent[unstructured.Unstructured](ctx, event)

		mapping, err := client.Mapper.RESTMapping(params.GK, params.Version)
		if err != nil {
			client.Mapper.Reset()
			return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for gk: %w", err)
		}

		resource, err := resourceCache.Get(event.Name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				ctrl.Logger(ctx).Info("resource not found")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
		}

		resourceIntf := func() dynamic.ResourceInterface {
			if mapping.Scope == meta.RESTScopeNamespace {
				return client.Dynamic.Resource(mapping.Resource).Namespace(event.Namespace)
			}
			return client.Dynamic.Resource(mapping.Resource)
		}()

		if resource.GetNamespace() == "" && mapping.Scope == meta.RESTScopeNamespace {
			resource.SetNamespace("default")
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set default namespace on flight: %w", err)
			}
			return ctrl.Result{}, nil
		}

		overrideMode := func() v1alpha1.AirwayMode {
			annotations := resource.GetAnnotations()
			if annotations == nil {
				return ""
			}
			override := v1alpha1.AirwayMode(annotations[flight.AnnotationOverrideMode])
			if !slices.Contains(v1alpha1.Modes(), override) {
				return ""
			}
			return override
		}()

		flightState, _ := params.States.LoadOrStore(event.String(), InstanceState{Mutex: new(sync.RWMutex)})

		// This lock ensures that admission cannot update subresources while this control loop is running.
		flightState.Mutex.Lock()
		defer flightState.Mutex.Unlock()

		defer func() {
			params.States.Store(event.String(), flightState)
		}()

		flightState.ClusterAccess = params.Airway.Spec.ClusterAccess
		flightState.Mode = cmp.Or(overrideMode, params.Airway.Spec.Mode, v1alpha1.AirwayModeStandard)

		setReadyCondition := func(status metav1.ConditionStatus, reason string, msg any) {
			if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				current, err := resourceIntf.Get(ctx, resource.GetName(), metav1.GetOptions{})
				if err != nil {
					if kerrors.IsNotFound(err) {
						return nil
					}
					return nil
				}

				if current.GetGeneration() != resource.GetGeneration() {
					// Don't update status if current generation has changed.
					return nil
				}

				conditions := internal.GetFlightConditions(current)

				if changed := meta.SetStatusCondition(&conditions, metav1.Condition{
					Type:               "Ready",
					Status:             status,
					ObservedGeneration: current.GetGeneration(),
					Reason:             reason,
					Message:            fmt.Sprintf("%v", msg),
				}); !changed {
					return nil
				}

				_ = unstructured.SetNestedField(current.Object, internal.MustUnstructuredObject[any](conditions), "status", "conditions")

				updated, err := resourceIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
				if err != nil {
					return err
				}

				resource = updated

				return nil
			}); err != nil {
				if kerrors.IsNotFound(err) {
					return
				}
				ctrl.Logger(ctx).Error("failed to set ready condition", "error", err)
			}
		}

		defer func() {
			if err != nil {
				setReadyCondition(metav1.ConditionFalse, "Error", err.Error())
			}
		}()

		if finalizers := resource.GetFinalizers(); resource.GetDeletionTimestamp() == nil && !slices.Contains(finalizers, cleanupFinalizer) {
			resource.SetFinalizers(append(finalizers, cleanupFinalizer))
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set cleanup finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if _, ok := internal.Find(resource.GetOwnerReferences(), func(ref metav1.OwnerReference) bool {
			return ref.Kind == v1alpha1.KindAirway && ref.APIVersion == v1alpha1.APIVersion && ref.Name == params.Airway.Name
		}); !ok {
			resource.SetOwnerReferences(append(resource.GetOwnerReferences(), metav1.OwnerReference{
				Kind:               v1alpha1.KindAirway,
				APIVersion:         v1alpha1.APIVersion,
				Name:               params.Airway.Name,
				UID:                params.Airway.UID,
				BlockOwnerDeletion: new(true),
			}))
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to add airway as owner reference: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !resource.GetDeletionTimestamp().IsZero() {
			setReadyCondition(metav1.ConditionFalse, "Terminating", "Mayday: Flight is being removed")

			if err := yoke.FromK8Client(client).Mayday(ctx, yoke.MaydayParams{
				Release:   ReleaseName(resource),
				Namespace: event.Namespace,
				PruneOpts: k8s.PruneOpts{
					RemoveCRDs:       params.Airway.Spec.Prune.CRDs,
					RemoveNamespaces: params.Airway.Spec.Prune.Namespaces,
				},
			}); err != nil {
				if !internal.IsWarning(err) {
					return ctrl.Result{}, fmt.Errorf("failed to run atc cleanup: %w", err)
				}
				ctrl.Logger(ctx).Warn("mayday succeeded despite a warning", "warning", err)
			}

			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				resource, err := resourceIntf.Get(ctx, resource.GetName(), metav1.GetOptions{})
				if err != nil {
					return err
				}
				finalizers := resource.GetFinalizers()
				if idx := slices.Index(finalizers, cleanupFinalizer); idx != -1 {
					resource.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
					_, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager})
					return err
				}
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}

			params.States.Delete(event.String())
			atc.dispatcher.RemoveEvent(event.WithoutMeta())

			return ctrl.Result{}, nil
		}

		object, _, err := unstructured.NestedFieldNoCopy(resource.Object, params.Airway.Spec.ObjectPath...)
		if err != nil {
			return ctrl.Result{}, ctrl.Terminal(fmt.Errorf("failed to get object path from: %q: %v", strings.Join(params.Airway.Spec.ObjectPath, ","), err))
		}

		data, err := json.Marshal(object)
		if err != nil {
			return ctrl.Result{}, ctrl.Terminal(fmt.Errorf("failed to marhshal resource: %w", err))
		}

		commander := yoke.FromK8Client(client)

		release := ReleaseName(resource)

		var identity *unstructured.Unstructured

		defer func() {
			if identity == nil || identity.Object["status"] == nil {
				return
			}

			if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				current, err := resourceIntf.Get(ctx, resource.GetName(), metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}
				if current.GetGeneration() != resource.GetGeneration() {
					return nil
				}

				// We don't want to change the identity itself as it is used later to check if we need to
				// spawn a readiness process.
				identity := identity.DeepCopy()

				current.Object["status"] = identity.Object["status"]

				conditions := internal.GetFlightConditions(resource)
				for _, cond := range internal.GetFlightConditions(identity) {
					meta.SetStatusCondition(&conditions, cond)
				}
				_ = unstructured.SetNestedField(current.Object, internal.MustUnstructuredObject[any](conditions), "status", "conditions")

				updated, err := resourceIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
				if err != nil {
					return err
				}
				resource = updated
				return nil
			}); err != nil {

				if kerrors.IsNotFound(err) {
					return
				}
				ctrl.Logger(ctx).Error("failed to update status for identity", "error", err)
			}
		}()

		takeoffParams := yoke.TakeoffParams{
			Release:   release,
			Namespace: event.Namespace,
			Checksum:  params.Airway.Spec.WasmURLs.FlightChecksum,
			Flight: yoke.FlightParams{
				Path:     params.Airway.Spec.WasmURLs.Flight,
				Insecure: params.Airway.Spec.Insecure,
				Input:    bytes.NewReader(data),
				Timeout:  params.Airway.Spec.Timeout.Duration,
			},
			ManagedBy:      "atc.yoke",
			Lock:           false,
			ForceConflicts: true,
			ForceOwnership: true,
			HistoryCapSize: cmp.Or(params.Airway.Spec.HistoryCapSize, 2),
			ClusterAccess: yoke.ClusterAccessParams{
				Enabled:          params.Airway.Spec.ClusterAccess,
				ResourceMatchers: params.Airway.Spec.ResourceAccessMatchers,
			},
			ExtraAnnotations: map[string]string{AnnotationInstanceRef: internal.ResourceRef(resource)},
			CrossNamespace:   params.Airway.Spec.Template.Scope == apiextv1.ClusterScoped,
			PruneOpts: k8s.PruneOpts{
				RemoveCRDs:       params.Airway.Spec.Prune.CRDs,
				RemoveNamespaces: params.Airway.Spec.Prune.Namespaces,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: resource.GetAPIVersion(),
					Kind:       resource.GetKind(),
					Name:       resource.GetName(),
					UID:        resource.GetUID(),
				},
			},
			IdentityFunc: func(item *unstructured.Unstructured) (ok bool) {
				defer func() {
					if ok {
						identity = item.DeepCopy()
					}
				}()
				return item.GroupVersionKind().GroupKind() == params.GK && item.GetName() == event.Name && item.GetNamespace() == event.Namespace
			},
		}

		if overrideURL, _, _ := unstructured.NestedString(resource.Object, "metadata", "annotations", flight.AnnotationOverrideFlight); overrideURL != "" {
			ctrl.Logger(ctx).Warn("using override module", "url", overrideURL)
			// Simply set the override URL as the flight path and let yoke load and execute the wasm module as if called from the command line.
			// We do not want to manually compile the module here or cache it, since this feature is for overrides that will be most often used in testing;
			// It is not recommended to override in production. As so it is allowable that users don't version the overrideURL and that the content can change.
			takeoffParams.Flight.Path = overrideURL
		} else {
			mod, err := atc.moduleCache.FromURL(
				ctx,
				cache.FromURLParams{
					URL:      params.Airway.Spec.WasmURLs.Flight,
					Checksum: params.Airway.Spec.WasmURLs.FlightChecksum,
					Insecure: params.Airway.Spec.Insecure,
					Attrs: cache.ModuleAttrs{
						MaxMemoryMib:    params.Airway.Spec.MaxMemoryMib,
						HostFunctionMap: host.BuildFunctionMap(client),
					},
				},
			)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to fetch flight module from cache: %w", err)
			}
			takeoffParams.Flight.Module = yoke.Module{
				Instance: mod,
				SourceMetadata: yoke.ModuleSourcetadata{
					Ref:      params.Airway.Spec.WasmURLs.Flight,
					Checksum: mod.Checksum(),
				},
			}
		}

		if flightState.Mode.IsDynamic() {
			ctx = host.WithResourceTracking(ctx)
			defer func() {
				if err == nil {
					// Takeoff succeeded, hence we want to drop all previous references to TrackedResources
					// and build a new fresh list. If there is an error, we are in a dirty state and we can keep the old resource
					// references as well as track whatever else was registered.
					atc.dispatcher.RemoveEvent(event.WithoutMeta())
				}
				for _, resource := range host.ExternalResources(ctx) {
					atc.dispatcher.Register(resource, event.WithoutMeta())
				}
			}()
		} else {
			// if we are not in dynamic mode, either via an update to the Airway or a removed annotation,
			// we need to stop EventDispatcher events to this controller.
			atc.dispatcher.RemoveEvent(event.WithoutMeta())
		}

		// We should always do resource tracking as this will allow us to pull the resource refs and check readiness.
		ctx = host.WithReleaseTracking(ctx)

		if flightState.Mode == v1alpha1.AirwayModeSubscription {
			defer func() {
				if err != nil {
					flightState.TrackedResources = flightState.TrackedResources.Union(host.InternalResources(ctx))
				} else {
					flightState.TrackedResources = host.InternalResources(ctx).Union(host.CandidateResources(ctx).Intersection(host.ReleaseResourcesRefs(ctx)))
				}
			}()
		} else {
			flightState.TrackedResources = nil
		}

		defer func() {
			if err != nil && internal.IsWarning(err) {
				ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
				err = nil
			}
		}()

		defer func() {
			if (err != nil && !internal.IsNoopErr(err)) || internal.GetFlightReadyCondition(identity) != nil {
				return
			}
			ready, readynessErr := func() (bool, error) {
				var errs []error
				var pending bool
				for _, resource := range host.ReleaseResources(ctx) {
					if ready, err := client.IsReady(ctx, resource); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", internal.ResourceRef(resource), err))
					} else if !ready {
						pending = true
						break
					}
				}
				if err := xerr.MultiErrFrom("failed to check release readiness", errs...); err != nil {
					return false, err
				}
				return !pending, nil
			}()
			if readynessErr != nil {
				err = readynessErr
				return
			}
			if ready {
				setReadyCondition(metav1.ConditionTrue, "Ready", "Successfully deployed")
			} else {
				setReadyCondition(metav1.ConditionFalse, "InProgress", fmt.Errorf("waiting for flight to become ready"))
				result.RequeueAfter = cmp.Or(min(result.RequeueAfter, 5*time.Second), 5*time.Second)
			}
		}()

		setReadyCondition(metav1.ConditionFalse, "InProgress", "Flight is taking off")

		return ctrl.Result{RequeueAfter: params.Airway.Spec.FixDriftInterval.Duration}, commander.Takeoff(ctx, takeoffParams)
	}

	return ctrl.Funcs{
		Handler:  reconciler,
		Teardown: func() {},
	}
}
