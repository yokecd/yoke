package atc

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/yoke"
)

func FlightReconciler() ctrl.HandleFunc {
	return func(ctx context.Context, e ctrl.Event) (ctrl.Result, error) {
		var (
			client     = ctrl.Client(ctx)
			commander  = yoke.FromK8Client(client)
			flightIntf = k8s.TypedInterface[v1alpha1.Flight](client.Dynamic, v1alpha1.FlightGVR()).Namespace(e.Namespace)
		)

		flight, err := flightIntf.Get(ctx, e.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get flight instance: %w", err)
		}

		if flight.DeletionTimestamp == nil && !slices.Contains(flight.Finalizers, cleanupFinalizer) {
			flight.Finalizers = append(flight.Finalizers, cleanupFinalizer)
			if _, err := flightIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to add finalizers: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !flight.DeletionTimestamp.IsZero() {
			if err := commander.Mayday(ctx, yoke.MaydayParams{
				Release:   flight.Name,
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

		if err := commander.Takeoff(ctx, yoke.TakeoffParams{
			ForceConflicts: true,
			ForceOwnership: true,
			CrossNamespace: false,
			Release:        flight.Name,
			Namespace:      flight.Namespace,
			Flight: yoke.FlightParams{
				Path:         flight.Spec.WasmURL,
				Args:         flight.Spec.Args,
				MaxMemoryMib: uint64(flight.Spec.MaxMemoryMib),
				Timeout:      flight.Spec.Timeout.Duration,
				Input:        strings.NewReader(flight.Spec.Input),
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
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform takeoff: %w", err)
		}

		return ctrl.Result{RequeueAfter: flight.Spec.FixDriftInterval.Duration}, nil
	}
}
