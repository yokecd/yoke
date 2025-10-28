package atc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
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

		airway = current

		readyCondition := metav1.Condition{
			Type:               "Ready",
			Status:             status,
			ObservedGeneration: airway.Generation,
			Reason:             reason,
			Message:            fmt.Sprintf("%v", msg),
		}

		conditions := airway.Status.Conditions

		i := slices.IndexFunc(conditions, func(condition metav1.Condition) bool {
			return condition.Type == "Ready"
		})

		readyCondition.LastTransitionTime = func() metav1.Time {
			if i < 0 || conditions[i].Status != status {
				return metav1.Now()
			}
			return conditions[i].LastTransitionTime
		}()

		if i < 0 {
			conditions = append(conditions, readyCondition)
		} else {
			conditions[i] = readyCondition
		}

		airway.Status.Conditions = conditions

		updated, err := airwayIntf.UpdateStatus(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager})
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

	modules := atc.moduleCache.Get(airway.Name)

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

			modules.LockAll()
			modules.Reset()
			modules.UnlockAll()

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
		modules.LockAll()
		defer modules.UnlockAll()

		modules.Reset()

		for _, value := range []struct {
			URL string
			Mod *wasm.Module
		}{
			{
				URL: airway.Spec.WasmURLs.Flight,
				Mod: modules.Flight,
			},
			{
				URL: airway.Spec.WasmURLs.Converter,
				Mod: modules.Converter,
			},
		} {
			if value.URL == "" {
				continue
			}
			data, err := yoke.LoadWasm(ctx, value.URL, airway.Spec.Insecure)
			if err != nil {
				return fmt.Errorf("failed to load wasm: %w", err)
			}
			mod, err := wasi.Compile(ctx, wasi.CompileParams{
				Wasm:            data,
				MaxMemoryMib:    airway.Spec.MaxMemoryMib,
				HostFunctionMap: host.BuildFunctionMap(ctrl.Client(ctx)),
			})
			if err != nil {
				return fmt.Errorf("failed to compile wasm: %w", err)
			}

			*value.Mod.Instance = mod
			value.Mod.SourceMetadata = yoke.ModuleSourcetadata{
				Ref:      value.URL,
				Checksum: internal.SHA1HexString(data),
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
			version.Schema.OpenAPIV3Schema.Properties["status"] = *openapi.SchemaFrom(reflect.TypeFor[struct {
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
				statusSchema.Properties["conditions"] = *openapi.SchemaFrom(reflect.TypeFor[flight.Conditions]())
			}
			if err := openapi.Satisfies(statusSchema.Properties["conditions"], *openapi.SchemaFrom(reflect.TypeFor[flight.Conditions]())); err != nil {
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
						Path:      ptr.To("/crdconvert/" + airway.Name),
						Port:      ptr.To(atc.service.Port),
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
						Path:      ptr.To("/validations/" + airway.Name),
						Port:      &atc.service.Port,
					},
					CABundle: atc.service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
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

	flightCtx, cancel := context.WithCancel(ctx)

	done := make(chan struct{})

	atc.cleanups[airway.Name] = func() {
		cancel()
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown in progress.")
		<-done
		delete(atc.cleanups, airway.Name)
	}

	flightGK := schema.GroupKind{
		Group: airway.Spec.Template.Group,
		Kind:  airway.Spec.Template.Names.Kind,
	}

	flightController, err := func() (Controller, error) {
		flightStates := new(xsync.Map[string, InstanceState])

		ctrl, err := ctrl.NewController(flightCtx, ctrl.Params{
			GK: flightGK,
			Handler: atc.InstanceReconciler(InstanceReconcilerParams{
				GK:      flightGK,
				Airway:  *airway,
				Version: storageVersion,
				Flight:  modules.Flight,
				States:  flightStates,
			}),
			Client:      ctrl.Client(ctx),
			Logger:      ctrl.RootLogger(ctx),
			Concurrency: atc.concurrency,
		})
		return Controller{Instance: ctrl, values: flightStates}, err
	}()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create flight controller: %w", err)
	}

	ctrl.Logger(ctx).Info("Launching flight controller")

	atc.controllers.Store(flightGK.String(), flightController)

	go func() {
		defer cancel()
		defer close(done)
		defer atc.controllers.Delete(flightGK.String())
		defer atc.dispatcher.RemoveController(flightController.Instance)

		airwayStatus(metav1.ConditionTrue, "Ready", "Flight-Controller launched")

		if err := flightController.Run(); err != nil {
			airwayStatus(metav1.ConditionFalse, "Error", fmt.Sprintf("Flight-Controller: %v", err))
			if errors.Is(err, context.Canceled) {
				ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown complete.", "groupKind", flightGK.String())
				return
			}
			ctrl.Logger(ctx).Error("could not process group kind", "error", err)
		}
	}()

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
