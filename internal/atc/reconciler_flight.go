package atc

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/davidmdm/x/xerr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/k8s/ctrl"
	"github.com/yokecd/yoke/pkg/yoke"
)

type TeardownFunc func()

func FlightReconciler(modules *cache.ModuleCache) ctrl.Funcs {
	return flightReconciler(modules, false)
}

func ClusterFlightReconsiler(modules *cache.ModuleCache) ctrl.Funcs {
	return flightReconciler(modules, true)
}

func flightReconciler(modules *cache.ModuleCache, clusterScope bool) ctrl.Funcs {
	gvr := func() schema.GroupVersionResource {
		if clusterScope {
			return v1alpha1.ClusterFlightGVR()
		}
		return v1alpha1.FlightGVR()
	}()

	reconciler := func(ctx context.Context, evt ctrl.Event) (result ctrl.Result, err error) {
		defer func() {
			if cache.IsDisallowedModuleError(err) {
				err = ctrl.Terminal(err)
			}
		}()

		// We use this type because it is the same as v1alpha1.Flight and ClusterFlight but we want to drop the convenience json marshalling methods
		type AltFlight v1alpha1.Flight

		var (
			client      = (*k8s.Client)(ctrl.Client(ctx))
			commander   = yoke.FromK8Client(client)
			flightIntf  = k8s.TypedInterface[AltFlight](client, gvr).Namespace(evt.Namespace)
			flightCache = ctrl.CacheFromEvent[AltFlight](ctx, evt)
		)

		flight, err := flightCache.Get(evt.Name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get flight instance: %w", err)
		}

		setReadyCondition := func(status metav1.ConditionStatus, reason string, msg any) {
			if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				current, err := flightIntf.Get(ctx, flight.GetName(), metav1.GetOptions{})
				if err != nil {
					if kerrors.IsNotFound(err) {
						return nil
					}
					return err
				}
				if current.GetGeneration() != flight.GetGeneration() {
					return nil
				}

				if changed := meta.SetStatusCondition((*[]metav1.Condition)(&current.Status.Conditions), metav1.Condition{
					Type:               "Ready",
					Status:             status,
					ObservedGeneration: flight.Generation,
					Reason:             reason,
					Message:            fmt.Sprintf("%v", msg),
				}); !changed {
					return nil
				}

				updated, err := flightIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
				if err != nil {
					if kerrors.IsNotFound(err) {
						return nil
					}
					return err
				}

				flight = updated
				return nil
			}); err != nil {
				ctrl.Logger(ctx).Error("failed to update flight status", "error", err)
			}
		}

		defer func() {
			if err != nil {
				setReadyCondition(metav1.ConditionFalse, "Error", err.Error())
			}
		}()

		if flight.DeletionTimestamp == nil && !slices.Contains(flight.Finalizers, cleanupFinalizer) {
			flight.Finalizers = append(flight.Finalizers, cleanupFinalizer)
			if _, err := flightIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to add finalizers: %w", err)
			}
			return ctrl.Result{}, nil
		}

		releasePrefix := fmt.Sprintf("%s/%s:", flight.Namespace, flight.GroupVersionKind().GroupKind())

		if !flight.DeletionTimestamp.IsZero() {
			setReadyCondition(metav1.ConditionFalse, "Terminating", "mayday is being performed")
			if err := commander.Mayday(ctx, yoke.MaydayParams{
				Release:   releasePrefix + flight.Name,
				Namespace: flight.Namespace,
				PruneOpts: yoke.PruneOpts{
					RemoveCRDs:       flight.Spec.Prune.CRDs,
					RemoveNamespaces: flight.Spec.Prune.Namespaces,
				},
			}); err != nil && !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to perform mayday: %w", err)
			}
			if idx := slices.Index(flight.Finalizers, cleanupFinalizer); idx >= 0 {
				flight.Finalizers = slices.Delete(flight.Finalizers, idx, idx+1)
				if _, err := flightIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove cleanup finalizer: %w", err)
				}
			}
			return ctrl.Result{}, nil
		}

		setReadyCondition(metav1.ConditionFalse, "InProgress", "fetching flight wasm module")

		mod, err := modules.FromURL(
			ctx,
			cache.FromURLParams{
				URL:      flight.Spec.WasmURL,
				Checksum: flight.Spec.Checksum,
				Insecure: flight.Spec.Insecure,
				Attrs: cache.ModuleAttrs{
					MaxMemoryMib:    flight.Spec.MaxMemoryMib,
					HostFunctionMap: host.BuildFunctionMap(client),
				},
			},
		)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get wasm module: %w", err)
		}

		takeoffParams := yoke.TakeoffParams{
			ForceConflicts: true,
			ForceOwnership: true,
			CrossNamespace: clusterScope,
			ReleasePrefix:  releasePrefix,
			Release:        flight.Name,
			Namespace:      flight.Namespace,
			Checksum:       flight.Spec.Checksum,
			Flight: yoke.FlightParams{
				Path:     flight.Spec.WasmURL,
				Insecure: flight.Spec.Insecure,
				Module: yoke.Module{
					Instance: mod,
					SourceMetadata: internal.Source{
						Ref:      flight.Spec.WasmURL,
						Checksum: mod.Checksum(),
					},
				},
				Args:         flight.Spec.Args,
				MaxMemoryMib: uint64(flight.Spec.MaxMemoryMib),
				Timeout:      flight.Spec.Timeout.Duration,
				Input:        v1alpha1.FlightInputStream(flight.Spec),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: flight.APIVersion,
					Kind:       flight.Kind,
					Name:       flight.Name,
					UID:        flight.UID,
				},
			},
			ClusterAccess: yoke.ClusterAccessParams{
				Enabled:          flight.Spec.ClusterAccess,
				ResourceMatchers: flight.Spec.ResourceAccessMatchers,
			},
			HistoryCapSize: cmp.Or(flight.Spec.HistoryCapSize, 2),
			ManagedBy:      "atc.yoke",
			PruneOpts: yoke.PruneOpts{
				RemoveCRDs:       flight.Spec.Prune.CRDs,
				RemoveNamespaces: flight.Spec.Prune.Namespaces,
			},
		}

		readinessByRef := map[string]bool{}

		ctx = host.WithReleaseTracking(ctx)

		defer func() {
			if err != nil {
				if !internal.IsWarning(err) {
					return
				}
				ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
				err = nil
			}

			items := func() []v1alpha1.InventoryItem {
				resources := host.ReleaseResources(ctx)
				items := make([]v1alpha1.InventoryItem, len(resources))
				for i, resource := range resources {
					gv, _ := schema.ParseGroupVersion(resource.GetAPIVersion())
					ref := internal.ResourceRef(resource)
					items[i] = v1alpha1.InventoryItem{
						Resource: ref,
						Version:  gv.Version,
						Ready:    readinessByRef[ref],
					}
				}
				return items
			}()

			if updateErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				current, err := flightIntf.Get(ctx, flight.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if current.Generation != flight.Generation {
					return nil
				}
				current.Status.Inventory = items
				_, err = flightIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
				return err
			}); updateErr != nil {
				err = fmt.Errorf("failed to update flight status with inventory: %w", updateErr)
			}
		}()

		defer func() {
			if err != nil && !internal.IsNoopErr(err) {
				return
			}
			ready, readynessErr := func() (bool, error) {
				var errs []error
				var pending bool
				for _, resource := range host.ReleaseResources(ctx) {
					ready, err := client.IsReady(ctx, resource)
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", internal.ResourceRef(resource), err))
						continue
					}
					readinessByRef[internal.ResourceRef(resource)] = ready
					if !ready {
						pending = true
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

		return ctrl.Result{RequeueAfter: flight.Spec.FixDriftInterval.Duration}, commander.Takeoff(ctx, takeoffParams)
	}

	return ctrl.Funcs{
		Handler:  reconciler,
		Teardown: func() {},
	}
}
