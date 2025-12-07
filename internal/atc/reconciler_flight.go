package atc

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
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
	cleanups := map[string]func(){}

	gvr := func() schema.GroupVersionResource {
		if clusterScope {
			return v1alpha1.ClusterFlightGVR()
		}
		return v1alpha1.FlightGVR()
	}()

	reconciler := func(ctx context.Context, evt ctrl.Event) (result ctrl.Result, err error) {
		// We use this type because it is the same as v1alpha1.Flight and ClusterFlight but we want to drop the convenience json marshalling methods
		type AltFlight v1alpha1.Flight

		var (
			client     = ctrl.Client(ctx)
			commander  = yoke.FromK8Client(client)
			flightIntf = k8s.TypedInterface[AltFlight](client.Dynamic, gvr).Namespace(evt.Namespace)
		)

		if cleanup := cleanups[evt.String()]; cleanup != nil {
			cleanup()
		}

		flight, err := flightIntf.Get(ctx, evt.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get flight instance: %w", err)
		}

		setReadyCondition := func(status metav1.ConditionStatus, reason string, msg any) {
			current, err := flightIntf.Get(ctx, flight.GetName(), metav1.GetOptions{})
			if err != nil {
				if kerrors.IsNotFound(err) {
					return
				}
				ctrl.Logger(ctx).Error("failed to update status", "error", err)
				return
			}
			if current.GetGeneration() != flight.GetGeneration() {
				return
			}

			meta.SetStatusCondition((*[]metav1.Condition)(&current.Status.Conditions), metav1.Condition{
				Type:               "Ready",
				Status:             status,
				ObservedGeneration: flight.Generation,
				Reason:             reason,
				Message:            fmt.Sprintf("%v", msg),
			})

			updated, err := flightIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
			if err != nil {
				if kerrors.IsNotFound(err) {
					return
				}
				ctrl.Logger(ctx).Error("failed to update flight status", "error", err)
				return
			}

			flight = updated
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

		mod, err := modules.FromURL(ctx, flight.Spec.WasmURL, cache.ModuleAttrs{
			MaxMemoryMib:    flight.Spec.MaxMemoryMib,
			HostFunctionMap: host.BuildFunctionMap(ctrl.Client(ctx)),
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get wasm module: %w", err)
		}

		setReadyCondition(metav1.ConditionFalse, "InProgress", "performing takeoff")

		if err := commander.Takeoff(ctx, yoke.TakeoffParams{
			ForceConflicts: true,
			ForceOwnership: true,
			CrossNamespace: clusterScope,
			ReleasePrefix:  releasePrefix,
			Release:        flight.Name,
			Namespace:      flight.Namespace,
			Flight: yoke.FlightParams{
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
		}); err != nil && !internal.IsWarning(err) {
			return ctrl.Result{}, fmt.Errorf("failed to perform takeoff: %w", err)
		}

		release, err := client.GetRelease(ctx, releasePrefix+flight.Name, flight.Namespace)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to lookup created revision: %w", err)
		}

		stages, err := client.GetRevisionResources(ctx, release.ActiveRevision())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get revision resources: %w", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)

		ctx, cancel := context.WithCancel(ctx)

		cleanups[evt.String()] = func() {
			cancel()
			wg.Wait()
		}

		e := make(chan error, 1)

		go func() {
			defer wg.Done()
			e <- ctrl.Client(ctx).WaitForReadyMany(ctx, stages.Flatten(), k8s.WaitOptions{
				Timeout:  k8s.NoTimeout,
				Interval: 2 * time.Second,
			})
		}()

		go func() {
			defer cancel()

			defer wg.Done()
			start := time.Now()

			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					setReadyCondition(
						metav1.ConditionFalse,
						"InProgress",
						fmt.Sprintf("Waiting for flight to become ready: elapsed: %s", time.Since(start).Round(time.Second)),
					)
				case err := <-e:
					if err != nil {
						setReadyCondition(metav1.ConditionFalse, "Error", fmt.Sprintf("Failed to wait for flight to become ready: %v", err))
					} else {
						setReadyCondition(metav1.ConditionTrue, "Ready", "Successfully deployed")
					}
					return
				}
			}
		}()

		return ctrl.Result{RequeueAfter: flight.Spec.FixDriftInterval.Duration}, nil
	}

	teardown := func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}

	return ctrl.Funcs{
		Handler:  reconciler,
		Teardown: teardown,
	}
}
