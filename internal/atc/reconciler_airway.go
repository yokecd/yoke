package atc

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

func (atc atc) Reconcile(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
	var (
		airwayIntf  = ctrl.Client(ctx).AirwayIntf
		webhookIntf = ctrl.Client(ctx).Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations()
	)

	airway, err := airwayIntf.Get(ctx, event.Name, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			ctrl.Logger(ctx).Info("airway not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get airway %s: %w", event.Name, err)
	}

	airwayStatus := func(status metav1.ConditionStatus, reason string, msg any) {
		current, err := airwayIntf.Get(ctx, airway.GetName(), metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				return
			}
			ctrl.Logger(ctx).Error("failed to update status", "error", err)
			return
		}

		if current.GetGeneration() != airway.GetGeneration() {
			// Don't update status if current generation has changed.
			return
		}

		meta.SetStatusCondition((*[]metav1.Condition)(&current.Status.Conditions), metav1.Condition{
			Type:               "Ready",
			Status:             status,
			ObservedGeneration: current.Generation,
			Reason:             reason,
			Message:            fmt.Sprintf("%v", msg),
		})

		updated, err := airwayIntf.UpdateStatus(ctx, current, metav1.UpdateOptions{FieldManager: fieldManager})
		if err != nil {
			if kerrors.IsNotFound(err) {
				return
			}
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}

		airway = updated
	}

	if airway.DeletionTimestamp == nil && !slices.Contains(airway.Finalizers, cleanupAirwayFinalizer) {
		finalizers := append(airway.Finalizers, cleanupAirwayFinalizer)
		airway.SetFinalizers(finalizers)
		if _, err := airwayIntf.Update(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add cleanup finalizer to airway: %v", err)
		}
		return ctrl.Result{}, nil
	}

	if airway.DeletionTimestamp != nil {
		airwayStatus(metav1.ConditionFalse, "Terminating", "cleaning up resources")

		if idx := slices.Index(airway.Finalizers, cleanupAirwayFinalizer); idx > -1 {
			if err := webhookIntf.Delete(ctx, airway.CRGroupResource().String(), metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to remove admission validation webhook: %w", err)
			}

			crdIntf := ctrl.Client(ctx).Dynamic.Resource(schema.GroupVersionResource{
				Group:    apiextv1.SchemeGroupVersion.Group,
				Version:  apiextv1.SchemeGroupVersion.Version,
				Resource: "customresourcedefinitions",
			})

			foregroundDelete := metav1.DeleteOptions{
				PropagationPolicy: ptr.To(metav1.DeletePropagationForeground),
			}

			if err := crdIntf.Delete(ctx, airway.Name, foregroundDelete); err != nil && !kerrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to remove custom resource definiton associated to airway: %v", err)
			}

			for attempt := range 10 {
				if _, err := crdIntf.Get(ctx, airway.Name, metav1.GetOptions{}); err != nil {
					if kerrors.IsNotFound(err) {
						break
					}
					return ctrl.Result{}, fmt.Errorf("failed to get CRD associated to airway: %v", err)
				}
				if attempt == 9 {
					return ctrl.Result{}, fmt.Errorf("termination is hung: crd is not being deleted: manual intervention may be needed")
				}
				time.Sleep(time.Second)
			}

			if cleanup := atc.cleanups[airway.Name]; cleanup != nil {
				cleanup()
			}

			finalizers := slices.Delete(airway.Finalizers, idx, idx+1)
			airway.SetFinalizers(finalizers)

			if _, err := airwayIntf.Update(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove cleanup finalizer to airway: %v", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if cleanup := atc.cleanups[airway.Name]; cleanup != nil {
		airwayStatus(metav1.ConditionFalse, "InProgress", "Cleaning up previous flight controller")
		cleanup()

		// cleanup will cause status changes to the airway. Refresh the airway before proceeding.
		airway, err = airwayIntf.Get(ctx, airway.GetName(), metav1.GetOptions{})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	airwayStatus(metav1.ConditionFalse, "InProgress", "Initializing flight controller")

	defer func() {
		if err != nil {
			airwayStatus(metav1.ConditionFalse, "Error", err)
		}
	}()

	if err := func() error {
		for _, value := range []string{
			airway.Spec.WasmURLs.Flight,
			airway.Spec.WasmURLs.Converter,
		} {
			if value == "" {
				continue
			}
			if _, err := atc.moduleCache.FromURL(ctx, value, cache.ModuleAttrs{
				MaxMemoryMib:    airway.Spec.MaxMemoryMib,
				HostFunctionMap: host.BuildFunctionMap(ctrl.Client(ctx)),
			}); err != nil {
				return fmt.Errorf("failed to warm cache: %w", err)
			}

			// Compiling a module creates a lot of heap usage that we don't need to hang onto
			// and that Go is loathe to release for no reason. Given that compiling is rare, it
			// is reasonable to let the runtime know this is an okay place to run GC.
			runtime.GC()
		}
		return nil
	}(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to setup cache: %w", err)
	}

	var storageVersion string
	for i := range airway.Spec.Template.Versions {
		version := &airway.Spec.Template.Versions[i]
		if version.Storage {
			storageVersion = version.Name
		}
		version.Subresources = &apiextv1.CustomResourceSubresources{
			Status: &apiextv1.CustomResourceSubresourceStatus{},
		}
		if version.Schema == nil {
			version.Schema = &apiextv1.CustomResourceValidation{}
		}
		if version.Schema.OpenAPIV3Schema == nil {
			version.Schema.OpenAPIV3Schema = &apiextv1.JSONSchemaProps{
				Type:       "object",
				Properties: apiextv1.JSONSchemaDefinitions{},
			}
		}
		if version.Schema.OpenAPIV3Schema.Properties == nil {
			version.Schema.OpenAPIV3Schema.Properties = map[string]apiextv1.JSONSchemaProps{}
		}
		statusSchema, ok := version.Schema.OpenAPIV3Schema.Properties["status"]
		if !ok {
			version.Schema.OpenAPIV3Schema.Properties["status"] = *(openapi.SchemaFor[struct {
				Conditions flight.Conditions `json:"conditions,omitempty"`
			}]())
		} else {
			if statusSchema.Type != "object" {
				return ctrl.Result{}, fmt.Errorf("invalid airway: status must be an object but got type: %q", statusSchema.Type)
			}
			if statusSchema.Properties == nil {
				statusSchema.Properties = map[string]apiextv1.JSONSchemaProps{}
			}
			if _, ok := statusSchema.Properties["conditions"]; !ok {
				statusSchema.Properties["conditions"] = *openapi.SchemaFor[flight.Conditions]()
			}
			if err := openapi.Satisfies(statusSchema.Properties["conditions"], *openapi.SchemaFor[flight.Conditions]()); err != nil {
				return ctrl.Result{}, fmt.Errorf("invalid airway: invalid status: conditions does not have expected schema: %v", err)
			}

			if idx := slices.Index(version.Schema.OpenAPIV3Schema.Required, "status"); idx >= 0 {
				version.Schema.OpenAPIV3Schema.Required = slices.Delete(version.Schema.OpenAPIV3Schema.Required, idx, idx+1)
			}
		}
	}

	if airway.Spec.WasmURLs.Converter != "" {
		airway.Spec.Template.Conversion = &apiextv1.CustomResourceConversion{
			Strategy: apiextv1.WebhookConverter,
			Webhook: &apiextv1.WebhookConversion{
				ClientConfig: &apiextv1.WebhookClientConfig{
					Service: &apiextv1.ServiceReference{
						Name:      atc.service.Name,
						Namespace: atc.service.Namespace,
						Path:      new("/crdconvert/" + airway.Name),
						Port:      new(atc.service.Port),
					},
					CABundle: atc.service.CABundle,
				},
				ConversionReviewVersions: []string{"v1"},
			},
		}
	}

	crd, err := internal.ToUnstructured(airway.CRD())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert airway CRD to unstructured object: %v", err)
	}

	if err := ctrl.Client(ctx).ApplyResource(ctx, crd, k8s.ApplyOpts{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply airway's template crd: %w", err)
	}

	if err := ctrl.Client(ctx).WaitForReady(ctx, crd, k8s.WaitOptions{Timeout: time.Minute, Interval: time.Second}); err != nil {
		return ctrl.Result{}, fmt.Errorf("airway's template crd failed to become ready: %w", err)
	}

	ctrl.Client(ctx).Mapper.Reset()

	validationWebhook := admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionregistrationv1.SchemeGroupVersion.Identifier(),
			Kind:       "ValidatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: airway.GetName(),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: airway.APIVersion,
					Kind:       airway.Kind,
					Name:       airway.Name,
					UID:        airway.UID,
				},
			},
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: airway.CRGroupResource().String(),
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: atc.service.Namespace,
						Name:      atc.service.Name,
						Path:      new("/validations/" + airway.Name),
						Port:      &atc.service.Port,
					},
					CABundle: atc.service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				MatchPolicy:             ptr.To(admissionregistrationv1.Exact),
				MatchConditions: []admissionregistrationv1.MatchCondition{
					{
						Name: "not-atc-service-account",
						Expression: fmt.Sprintf(
							`request.userInfo.username != "system:serviceaccount:%s:%s-service-account"`,
							atc.service.Namespace,
							atc.service.Name,
						),
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{airway.Spec.Template.Group},
							APIVersions: []string{storageVersion},
							Resources:   []string{airway.Spec.Template.Names.Plural},
							Scope: func() *admissionregistrationv1.ScopeType {
								if airway.Spec.Template.Scope == apiextv1.ClusterScoped {
									return ptr.To(admissionregistrationv1.ClusterScope)
								}
								return ptr.To(admissionregistrationv1.NamespacedScope)
							}(),
						},
					},
				},
			},
		},
	}

	rawValidationWebhook, err := json.Marshal(validationWebhook)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to serialize validation webhook: %w", err)
	}

	if _, err := webhookIntf.Patch(ctx, validationWebhook.Name, types.ApplyPatchType, rawValidationWebhook, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create validation webhook: %w", err)
	}

	flightGK := schema.GroupKind{
		Group: airway.Spec.Template.Group,
		Kind:  airway.Spec.Template.Names.Kind,
	}

	atc.cleanups[airway.Name] = func() {
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown in progress.")
		ctrl.Inst(ctx).ShutdownGK(flightGK)
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown complete.")
		delete(atc.cleanups, airway.Name)
	}

	ctrl.Logger(ctx).Info("Launching flight controller")

	reconcilerParams := InstanceReconcilerParams{
		GK:      flightGK,
		Airway:  *airway,
		Version: storageVersion,
		States:  atc.flightStates,
	}

	if err := ctrl.Inst(ctx).RegisterGK(flightGK, atc.InstanceReconciler(reconcilerParams)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to register flight controller for gk: %w", err)
	}

	airwayStatus(metav1.ConditionTrue, "Ready", "Flight-Controller launched")

	return ctrl.Result{}, nil
}

func ReleaseName(resource *unstructured.Unstructured) string {
	gvk := resource.GroupVersionKind()
	elems := []string{
		gvk.Group,
		gvk.Kind,
	}

	if ns := resource.GetNamespace(); ns != "" {
		elems = append(elems, ns)
	}

	elems = append(elems, resource.GetName())

	return strings.Join(elems, ".")
}
