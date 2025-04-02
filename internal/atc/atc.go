package atc

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

const (
	fieldManager           = "yoke.cd/atc"
	cleanupFinalizer       = "yoke.cd/mayday.flight"
	cleanupAirwayFinalizer = "yoke.cd/strip.airway"
)

type Controller struct {
	*ctrl.Instance
	modes map[string]v1alpha1.AirwayMode
}

func (controller Controller) FlightMode(name, ns string) v1alpha1.AirwayMode {
	return controller.modes[ctrl.Event{Name: name, Namespace: ns}.String()]
}

type ControllerCache = xsync.Map[string, Controller]

func GetReconciler(airway schema.GroupKind, service ServiceDef, cache *wasm.ModuleCache, controllers *ControllerCache, concurrency int) (ctrl.HandleFunc, func()) {
	atc := atc{
		airway:      airway,
		concurrency: concurrency,
		service:     service,
		cleanups:    map[string]func(){},
		moduleCache: cache,
		controllers: controllers,
	}
	return atc.Reconcile, atc.Teardown
}

type atc struct {
	airway      schema.GroupKind
	concurrency int

	controllers *ControllerCache
	service     ServiceDef
	cleanups    map[string]func()
	moduleCache *wasm.ModuleCache
}

func (atc atc) Reconcile(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
	mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(atc.airway)
	if err != nil {
		ctrl.Client(ctx).Mapper.Reset()
		return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for groupkind %s: %w", atc.airway, err)
	}

	var (
		airwayIntf  = ctrl.Client(ctx).Dynamic.Resource(mapping.Resource)
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

	var typedAirway v1alpha1.Airway
	if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
		return ctrl.Result{}, err
	}

	airwayStatus := func(status string, msg any) {
		var err error
		airway, err = airwayIntf.Get(ctx, event.Name, metav1.GetOptions{})
		if err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", fmt.Errorf("failed to get airway: %v", err))
			return
		}

		_ = unstructured.SetNestedMap(
			airway.Object,
			internal.MustUnstructuredObject(flight.Status{Status: status, Msg: fmt.Sprintf("%v", msg)}),
			"status",
		)

		updated, err := airwayIntf.UpdateStatus(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager})
		if err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}

		airway = updated

		if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}
	}

	if typedAirway.DeletionTimestamp == nil && !slices.Contains(typedAirway.Finalizers, cleanupAirwayFinalizer) {
		finalizers := append(typedAirway.Finalizers, cleanupAirwayFinalizer)
		airway.SetFinalizers(finalizers)
		if _, err := airwayIntf.Update(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add cleanup finalizer to airway: %v", err)
		}
		return ctrl.Result{}, nil
	}

	modules := atc.moduleCache.Get(typedAirway.Name)

	if typedAirway.DeletionTimestamp != nil {
		airwayStatus("Terminating", "cleaning up resources")

		if idx := slices.Index(typedAirway.Finalizers, cleanupAirwayFinalizer); idx > -1 {
			if err := webhookIntf.Delete(ctx, typedAirway.CRGroupResource().String(), metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
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

			if err := crdIntf.Delete(ctx, typedAirway.Name, foregroundDelete); err != nil && !kerrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to remove custom resource definiton associated to airway: %v", err)
			}

			for attempt := range 10 {
				if _, err := crdIntf.Get(ctx, typedAirway.Name, metav1.GetOptions{}); err != nil {
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

			if cleanup := atc.cleanups[typedAirway.Name]; cleanup != nil {
				cleanup()
			}

			modules.LockAll()
			modules.Reset()
			modules.UnlockAll()

			finalizers := slices.Delete(typedAirway.Finalizers, idx, idx+1)
			airway.SetFinalizers(finalizers)

			if _, err := airwayIntf.Update(ctx, airway, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove cleanup finalizer to airway: %v", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if cleanup := atc.cleanups[typedAirway.Name]; cleanup != nil {
		airwayStatus("InProgress", "Cleaning up previous flight controller")
		cleanup()

		// cleanup will cause status changes to the airway. Refresh the airway before proceeding.
		airway, err = airwayIntf.Get(ctx, airway.GetName(), metav1.GetOptions{})
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
			return ctrl.Result{}, err
		}
	}

	airwayStatus("InProgress", "Initializing flight controller")

	defer func() {
		if err != nil {
			airwayStatus("Error", err)
		}
	}()

	if err := func() error {
		modules.LockAll()
		defer modules.UnlockAll()

		modules.Reset()

		for _, value := range []struct {
			URL string
			Mod *wasi.Module
		}{
			{
				URL: typedAirway.Spec.WasmURLs.Flight,
				Mod: modules.Flight.Module,
			},
			{
				URL: typedAirway.Spec.WasmURLs.Converter,
				Mod: modules.Converter.Module,
			},
		} {
			if value.URL == "" {
				continue
			}
			data, err := yoke.LoadWasm(ctx, value.URL, typedAirway.Spec.Insecure)
			if err != nil {
				return fmt.Errorf("failed to load wasm: %w", err)
			}
			mod, err := wasi.Compile(ctx, wasi.CompileParams{
				Wasm:   data,
				Client: ctrl.Client(ctx),
			})
			if err != nil {
				return fmt.Errorf("failed to compile wasm: %w", err)
			}
			*value.Mod = mod

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
	for i := range typedAirway.Spec.Template.Versions {
		version := &typedAirway.Spec.Template.Versions[i]
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
		version.Schema.OpenAPIV3Schema.Properties["status"] = *openapi.SchemaFrom(reflect.TypeFor[flight.Status]())
	}

	if typedAirway.Spec.WasmURLs.Converter != "" {
		typedAirway.Spec.Template.Conversion = &apiextv1.CustomResourceConversion{
			Strategy: apiextv1.WebhookConverter,
			Webhook: &apiextv1.WebhookConversion{
				ClientConfig: &apiextv1.WebhookClientConfig{
					Service: &apiextv1.ServiceReference{
						Name:      atc.service.Name,
						Namespace: atc.service.Namespace,
						Path:      ptr.To("/crdconvert/" + typedAirway.Name),
						Port:      ptr.To(atc.service.Port),
					},
					CABundle: atc.service.CABundle,
				},
				ConversionReviewVersions: []string{"v1"},
			},
		}
	}

	crd, err := internal.ToUnstructured(typedAirway.CRD())
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
					APIVersion: typedAirway.APIVersion,
					Kind:       typedAirway.Kind,
					Name:       typedAirway.Name,
					UID:        typedAirway.UID,
				},
			},
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: typedAirway.CRGroupResource().String(),
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: atc.service.Namespace,
						Name:      atc.service.Name,
						Path:      ptr.To("/validations/" + typedAirway.Name),
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
							APIGroups:   []string{typedAirway.Spec.Template.Group},
							APIVersions: []string{storageVersion},
							Resources:   []string{typedAirway.Spec.Template.Names.Plural},
							Scope: func() *admissionregistrationv1.ScopeType {
								if typedAirway.Spec.Template.Scope == apiextv1.ClusterScoped {
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

	atc.cleanups[typedAirway.Name] = func() {
		cancel()
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown in progress.")
		<-done
		delete(atc.cleanups, typedAirway.Name)
	}

	flightGK := schema.GroupKind{
		Group: typedAirway.Spec.Template.Group,
		Kind:  typedAirway.Spec.Template.Names.Kind,
	}

	flightController, err := func() (Controller, error) {
		modes := map[string]v1alpha1.AirwayMode{}

		ctrl, err := ctrl.NewController(flightCtx, ctrl.Params{
			GK: flightGK,
			Handler: atc.FlightReconciler(FlightReconcilerParams{
				GK:      flightGK,
				Airway:  typedAirway,
				Version: storageVersion,
				Flight:  modules.Flight,
				Modes:   modes,
			}),
			Client:      ctrl.Client(ctx),
			Logger:      ctrl.RootLogger(ctx),
			Concurrency: atc.concurrency,
		})
		return Controller{Instance: ctrl, modes: modes}, err
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

		airwayStatus("Ready", "Flight-Controller launched")

		if err := flightController.Run(); err != nil {
			airwayStatus("Error", fmt.Sprintf("Flight-Controller: %v", err))
			if errors.Is(err, context.Canceled) {
				ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown complete.", "groupKind", flightGK.String())
				return
			}
			ctrl.Logger(ctx).Error("could not process group kind", "error", err)
		}
	}()

	return ctrl.Result{}, nil
}

func (atc atc) Teardown() {
	for _, cleanup := range atc.cleanups {
		cleanup()
	}
}

type FlightReconcilerParams struct {
	GK      schema.GroupKind
	Version string
	Flight  *wasm.Module
	Airway  v1alpha1.Airway
	Modes   map[string]v1alpha1.AirwayMode
}

func (atc atc) FlightReconciler(params FlightReconcilerParams) ctrl.HandleFunc {
	pollerCleanups := map[string]func(){}

	return func(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
		ctx = internal.WithStdio(ctx, io.Discard, io.Discard, os.Stdin)

		mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(params.GK, params.Version)
		if err != nil {
			ctrl.Client(ctx).Mapper.Reset()
			return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for gk: %w", err)
		}

		resourceIntf := func() dynamic.ResourceInterface {
			if mapping.Scope == meta.RESTScopeNamespace {
				return ctrl.Client(ctx).Dynamic.Resource(mapping.Resource).Namespace(event.Namespace)
			}
			return ctrl.Client(ctx).Dynamic.Resource(mapping.Resource)
		}()

		resource, err := resourceIntf.Get(ctx, event.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				ctrl.Logger(ctx).Info("resource not found")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
		}

		if resource.GetNamespace() == "" && mapping.Scope == meta.RESTScopeNamespace {
			resource.SetNamespace("default")
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set default namespace on flight: %w", err)
			}

			return ctrl.Result{}, nil
		}

		overrideMode := func() v1alpha1.AirwayMode {
			labels := resource.GetLabels()
			if labels == nil {
				return ""
			}
			override := v1alpha1.AirwayMode(labels[flight.AnnotationOverrideMode])
			if !slices.Contains(v1alpha1.Modes(), override) {
				return ""
			}
			return override
		}()

		params.Modes[event.String()] = cmp.Or(overrideMode, params.Airway.Spec.Mode, v1alpha1.AirwayModeStandard)

		flightStatus := func(status string, msg any) {
			var err error
			resource, err = resourceIntf.Get(ctx, event.Name, metav1.GetOptions{})
			if err != nil {
				ctrl.Logger(ctx).Error("failed to update flight status", "error", fmt.Errorf("failed to get flight: %v", err))
				return
			}

			_ = unstructured.SetNestedMap(
				resource.Object,
				internal.MustUnstructuredObject(flight.Status{Status: status, Msg: fmt.Sprintf("%v", msg)}),
				"status",
			)

			updated, err := resourceIntf.UpdateStatus(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager})
			if err != nil {
				ctrl.Logger(ctx).Error("failed to update flight status", "error", err)
				return
			}

			resource = updated
		}

		defer func() {
			if err != nil {
				flightStatus("Error", err.Error())
			}
		}()

		if finalizers := resource.GetFinalizers(); resource.GetDeletionTimestamp() == nil && !slices.Contains(finalizers, cleanupFinalizer) {
			resource.SetFinalizers(append(finalizers, cleanupFinalizer))
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set cleanup finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !resource.GetDeletionTimestamp().IsZero() {
			flightStatus("Terminating", "Mayday: Flight is being removed")

			if err := yoke.FromK8Client(ctrl.Client(ctx)).Mayday(ctx, ReleaseName(resource), event.Namespace); err != nil {
				if !internal.IsWarning(err) {
					return ctrl.Result{}, fmt.Errorf("failed to run atc cleanup: %w", err)
				}
				ctrl.Logger(ctx).Warn("mayday succeeded despite a warning", "warning", err)
			}

			finalizers := resource.GetFinalizers()
			if idx := slices.Index(finalizers, cleanupFinalizer); idx != -1 {
				resource.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
				if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
				}
			}

			return ctrl.Result{}, nil
		}

		object, _, err := unstructured.NestedFieldNoCopy(resource.Object, params.Airway.Spec.ObjectPath...)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get object path from: %q: %v", strings.Join(params.Airway.Spec.ObjectPath, ","), err)
		}

		data, err := json.Marshal(object)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to marhshal resource: %w", err)
		}

		commander := yoke.FromK8Client(ctrl.Client(ctx))

		release := ReleaseName(resource)

		takeoffParams := yoke.TakeoffParams{
			Release: release,
			Flight: yoke.FlightParams{
				Input:     bytes.NewReader(data),
				Namespace: event.Namespace,
			},
			ClusterAccess:  params.Airway.Spec.ClusterAccess,
			CrossNamespace: params.Airway.Spec.CrossNamespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: resource.GetAPIVersion(),
					Kind:       resource.GetKind(),
					Name:       resource.GetName(),
					UID:        resource.GetUID(),
				},
			},
		}

		if overrideURL, _, _ := unstructured.NestedString(resource.Object, "metadata", "annotations", flight.AnnotationOverrideFlight); overrideURL != "" {
			ctrl.Logger(ctx).Warn("using override module", "url", overrideURL)
			// Simply set the override URL as the flight path and let yoke load and execute the wasm module as if called from the command line.
			// We do not want to manually compile the module here or cache it, since this feature is for overrides that will be most often used in testing;
			// It is not recommended to override in production. As so it is allowable that users don't version the overrideURL and that the content can change.
			takeoffParams.Flight.Path = overrideURL
		} else {
			params.Flight.RLock()
			defer params.Flight.RUnlock()
			takeoffParams.Flight.Module = params.Flight.Module
		}

		flightStatus("InProgress", "Flight is taking off")

		if err := commander.Takeoff(ctx, takeoffParams); err != nil {
			if !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to takeoff: %w", err)
			}
			ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
		}

		if event.Drift || params.Airway.Spec.FixDriftInterval > 0 {
			flightStatus("InProgress", "Fixing drift / turbulence")
			if err := commander.Turbulence(ctx, yoke.TurbulenceParams{
				Release:   release,
				Namespace: event.Namespace,
				Fix:       true,
				Silent:    true,
			}); err != nil && !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to fix drift: %w", err)
			}
		}

		if err := func() (err error) {
			release, err := ctrl.Client(ctx).GetRelease(ctx, release, event.Namespace)
			if err != nil {
				return err
			}
			if len(release.History) == 0 {
				return fmt.Errorf("release not found")
			}

			resources, err := ctrl.Client(ctx).GetRevisionResources(ctx, release.ActiveRevision())
			if err != nil {
				return fmt.Errorf("failed to get release resources: %w", err)
			}

			if cleanup := pollerCleanups[event.String()]; cleanup != nil {
				cleanup()
			}

			var wg sync.WaitGroup
			wg.Add(2)

			ctx, cancel := context.WithCancel(ctx)

			pollerCleanups[event.String()] = func() {
				cancel()
				wg.Wait()
			}

			e := make(chan error, 1)

			go func() {
				defer wg.Done()
				e <- ctrl.Client(ctx).WaitForReadyMany(ctx, resources.Flatten(), k8s.WaitOptions{
					Timeout:  k8s.NoTimeout,
					Interval: 2 * time.Second,
				})
			}()

			go func() {
				// Release resources if no longer polling.
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
						flightStatus(
							"InProgress",
							fmt.Sprintf("Waiting for flight to become ready: elapsed: %s", time.Since(start).Round(time.Second)),
						)
					case err := <-e:
						if err != nil {
							flightStatus("Error", fmt.Sprintf("Failed to wait for flight to become ready: %v", err))
						} else {
							flightStatus("Ready", "Successfully deployed")
						}
						return
					}
				}
			}()

			return nil
		}(); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: params.Airway.Spec.FixDriftInterval.Duration()}, nil
	}
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

type ServiceDef struct {
	Name      string
	Namespace string
	CABundle  []byte
	Port      int32
}
