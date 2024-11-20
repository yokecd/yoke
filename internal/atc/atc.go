package atc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

const (
	fieldManager     = "yoke.cd/atc"
	cleanupFinalizer = "yoke.cd/mayday.flight"
)

func GetReconciler(airway schema.GroupKind, cacheDir string, concurrency int) (ctrl.HandleFunc, func()) {
	atc := atc{
		Airway:      airway,
		CacheDir:    cacheDir,
		Concurrency: concurrency,
		cleanups:    map[string]func(){},
		locks:       &sync.Map{},
	}
	return atc.Reconcile, atc.Teardown
}

type atc struct {
	Airway      schema.GroupKind
	CacheDir    string
	Concurrency int

	cleanups map[string]func()
	locks    *sync.Map
}

func (atc atc) Reconcile(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
	mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(atc.Airway)
	if err != nil {
		ctrl.Client(ctx).Mapper.Reset()
		return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for groupkind %s: %w", atc.Airway, err)
	}

	airwayIntf := ctrl.Client(ctx).Dynamic.Resource(mapping.Resource)

	airway, err := airwayIntf.Get(ctx, event.Name, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			ctrl.Logger(ctx).Info("airway not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get airway %s: %w", event.Name, err)
	}

	var typedAirway v1alpha1.Airway
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
		return ctrl.Result{}, err
	}

	airwayStatus := func(status string, msg any) {
		airway, err := airwayIntf.Get(ctx, airway.GetName(), metav1.GetOptions{})
		if err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", fmt.Errorf("failed to get airway: %v", err))
			return
		}

		_ = unstructured.SetNestedMap(
			airway.Object,
			unstructuredObject(v1alpha1.AirwayStatus{Status: status, Msg: fmt.Sprintf("%v", msg)}).(map[string]any),
			"status",
		)

		updated, err := airwayIntf.UpdateStatus(ctx, airway.DeepCopy(), metav1.UpdateOptions{FieldManager: fieldManager})
		if err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}

		airway = updated

		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}
	}

	defer func() {
		if err != nil {
			airwayStatus("Error", err)
		}
	}()

	cacheDir := filepath.Join("./cache", airway.GetName())
	wasmPath := filepath.Join(cacheDir, "source.wasm")

	wasm, err := yoke.LoadWasm(ctx, typedAirway.Spec.WasmURL)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to load wasm: %w", err)
	}

	mutex := func() *sync.RWMutex {
		value, _ := atc.locks.LoadOrStore(airway.GetName(), new(sync.RWMutex))
		return value.(*sync.RWMutex)
	}()

	if err := func() error {
		mutex.Lock()
		defer mutex.Unlock()

		if err := wasi.Compile(ctx, wasi.CompileParams{Wasm: wasm, CacheDir: cacheDir}); err != nil {
			return fmt.Errorf("failed to compile wasm: %w", err)
		}

		if err := os.WriteFile(wasmPath, wasm, 0o644); err != nil {
			return fmt.Errorf("failed to cache wasm asset: %w", err)
		}

		return nil
	}(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to setup cache: %w", err)
	}

	for i := range typedAirway.Spec.Template.Versions {
		version := &typedAirway.Spec.Template.Versions[i]
		if !version.Storage {
			continue
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
		version.Schema.OpenAPIV3Schema.Properties["status"] = *openapi.SchemaFrom(reflect.TypeFor[FlightStatus]())
		break
	}

	crd := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": typedAirway.Name,
			},
			"spec": unstructuredObject(typedAirway.Spec.Template),
		},
	}

	if err := ctrl.Client(ctx).ApplyResource(ctx, crd, k8s.ApplyOpts{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply airway's template crd: %w", err)
	}

	if err := ctrl.Client(ctx).WaitForReady(ctx, crd, k8s.WaitOptions{Timeout: time.Minute, Interval: time.Second}); err != nil {
		return ctrl.Result{}, fmt.Errorf("airway's template crd failed to become ready: %w", err)
	}

	if cleanup := atc.cleanups[typedAirway.Name]; cleanup != nil {
		cleanup()
	}

	flightCtx, cancel := context.WithCancel(ctx)

	done := make(chan struct{})

	atc.cleanups[typedAirway.Name] = func() {
		cancel()
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown in progress.")
		<-done
	}

	flightController := ctrl.Instance{
		Client:      ctrl.Client(ctx),
		Logger:      ctrl.RootLogger(ctx),
		Concurrency: atc.Concurrency,
	}

	flightGK := schema.GroupKind{
		Group: typedAirway.Spec.Template.Group,
		Kind:  typedAirway.Spec.Template.Names.Kind,
	}

	flightReconciler := atc.FlightReconciler(FlightReconcilerParams{
		GK:               flightGK,
		WasmPath:         wasmPath,
		CacheDir:         cacheDir,
		Lock:             mutex,
		FixDriftInterval: typedAirway.Spec.FixDriftInterval.Duration(),
		CreateCrds:       typedAirway.Spec.CreateCRDs,
	})

	go func() {
		defer cancel()
		defer close(done)

		airwayStatus("Ready", "Flight-Controller launched")

		if err := flightController.ProcessGroupKind(flightCtx, flightGK, flightReconciler); err != nil {
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
	GK               schema.GroupKind
	WasmPath         string
	CacheDir         string
	Lock             *sync.RWMutex
	FixDriftInterval time.Duration
	CreateCrds       bool
	ObjectPath       []string
}

func (atc atc) FlightReconciler(params FlightReconcilerParams) ctrl.HandleFunc {
	return func(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
		mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(params.GK)
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

		flight, err := resourceIntf.Get(ctx, event.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				ctrl.Logger(ctx).Info("resource not found")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
		}

		flightStatus := func(status string, msg any) {
			flight, err := resourceIntf.Get(ctx, flight.GetName(), metav1.GetOptions{})
			if err != nil {
				ctrl.Logger(ctx).Error("failed to update flight status", "error", fmt.Errorf("failed to get flight: %v", err))
				return
			}

			_ = unstructured.SetNestedMap(
				flight.Object,
				unstructuredObject(FlightStatus{Status: status, Msg: fmt.Sprintf("%v", msg)}).(map[string]any),
				"status",
			)

			updated, err := resourceIntf.UpdateStatus(ctx, flight.DeepCopy(), metav1.UpdateOptions{FieldManager: fieldManager})
			if err != nil {
				ctrl.Logger(ctx).Error("failed to update flight status", "error", err)
				return
			}

			flight = updated
		}

		defer func() {
			if err != nil {
				flightStatus("Error", err.Error())
			}
		}()

		if finalizers := flight.GetFinalizers(); flight.GetDeletionTimestamp() == nil && !slices.Contains(finalizers, cleanupFinalizer) {
			flight.SetFinalizers(append(finalizers, cleanupFinalizer))
			if _, err := resourceIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set cleanup finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !flight.GetDeletionTimestamp().IsZero() {
			flightStatus("Terminating", "Mayday: Flight is being removed")

			if err := yoke.FromK8Client(ctrl.Client(ctx)).Mayday(ctx, event.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to run atc cleanup: %w", err)
			}

			finalizers := flight.GetFinalizers()
			if idx := slices.Index(finalizers, cleanupFinalizer); idx != -1 {
				flight.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
				if _, err := resourceIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
				}
			}

			return ctrl.Result{}, nil
		}

		object, _, err := unstructured.NestedFieldNoCopy(flight.Object, params.ObjectPath...)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get object path from: %q: %v", strings.Join(params.ObjectPath, ","), err)
		}

		data, err := json.Marshal(object)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to marhshal resource: %w", err)
		}

		params.Lock.RLock()
		defer params.Lock.RUnlock()

		commander := yoke.FromK8Client(ctrl.Client(ctx))

		takeoffParams := yoke.TakeoffParams{
			Release: event.String(),
			Flight: yoke.FlightParams{
				Path:                params.WasmPath,
				Input:               bytes.NewReader(data),
				Namespace:           event.Namespace,
				CompilationCacheDir: params.CacheDir,
			},
			CreateCRDs: params.CreateCrds,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: flight.GetAPIVersion(),
					Kind:       flight.GetKind(),
					Name:       flight.GetName(),
					UID:        flight.GetUID(),
				},
			},
		}

		flightStatus("InProgress", "Flight is taking off")

		if err := commander.Takeoff(ctx, takeoffParams); err != nil {
			if !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to takeoff: %w", err)
			}
			ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
		}

		if params.FixDriftInterval > 0 {
			flightStatus("InProgress", "Fixing drift / turbulence")
			if err := commander.Turbulence(ctx, yoke.TurbulenceParams{
				Release: event.String(),
				Fix:     true,
				Silent:  true,
			}); err != nil && !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to fix drift: %w", err)
			}
		}

		flightStatus("Ready", "Successfully deployed")

		return ctrl.Result{RequeueAfter: params.FixDriftInterval}, nil
	}
}

func unstructuredObject(value any) any {
	data, _ := json.Marshal(value)
	var result any
	_ = json.Unmarshal(data, &result)
	return result
}

type FlightStatus struct {
	Status string `json:"status"`
	Msg    string `json:"msg"`
}
