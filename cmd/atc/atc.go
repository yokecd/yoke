package main

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
	"sync"
	"time"

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
	"github.com/yokecd/yoke/pkg/yoke"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
)

type ATC struct {
	Airway      schema.GroupKind
	CacheDir    string
	Concurrency int
	Cleanups    map[string]func()
	Locks       *sync.Map
	Prev        map[string]any
}

func MakeATC(airway schema.GroupKind, cacheDir string, concurrency int) (ATC, func()) {
	atc := ATC{
		Airway:      airway,
		CacheDir:    cacheDir,
		Concurrency: concurrency,
		Cleanups:    map[string]func(){},
		Locks:       &sync.Map{},
		Prev:        map[string]any{},
	}
	return atc, atc.Teardown
}

func (atc ATC) Reconcile(ctx context.Context, event ctrl.Event) (result ctrl.Result, err error) {
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

	if prevSpec := atc.Prev[airway.GetName()]; prevSpec != nil && reflect.DeepEqual(airwaySpec(airway), prevSpec) {
		ctrl.Logger(ctx).Info("airway status update: skip reconcile loop")
		return ctrl.Result{}, nil
	}

	defer func() {
		if err == nil {
			atc.Prev[airway.GetName()] = airwaySpec(airway)
		}
	}()

	airwayStatus := func(status, msg string) {
		_ = unstructured.SetNestedMap(airway.Object, map[string]any{"Status": status, "Msg": msg}, "status")
		updated, err := airwayIntf.UpdateStatus(ctx, airway.DeepCopy(), metav1.UpdateOptions{FieldManager: "yoke.cd/atc"})
		if err != nil {
			ctrl.Logger(ctx).Error("failed to update airway status", "error", err)
			return
		}
		airway = updated
	}

	airwayStatus("InProgress", "Reconciliation started")

	defer func() {
		if err != nil {
			airwayStatus("Error", err.Error())
		}
	}()

	var typedAirway v1alpha1.Airway
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway); err != nil {
		return ctrl.Result{}, err
	}

	cacheDir := filepath.Join("./cache", airway.GetName())
	wasmPath := filepath.Join(cacheDir, "source.wasm")

	wasm, err := yoke.LoadWasm(ctx, typedAirway.Spec.WasmURL)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to load wasm: %w", err)
	}

	mutex := func() *sync.RWMutex {
		value, _ := atc.Locks.LoadOrStore(airway.GetName(), new(sync.RWMutex))
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

	spec, _, _ := unstructured.NestedMap(airway.Object, "spec", "template")

	crd := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": airway.GetName(),
			},
			"spec": spec,
		},
	}

	if err := ctrl.Client(ctx).ApplyResource(ctx, crd, k8s.ApplyOpts{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply airway's template crd: %w", err)
	}

	if err := ctrl.Client(ctx).WaitForReady(ctx, crd, k8s.WaitOptions{Timeout: time.Minute, Interval: time.Second}); err != nil {
		return ctrl.Result{}, fmt.Errorf("airway's template crd failed to become ready: %w", err)
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

	flightHander := func(ctx context.Context, event ctrl.Event) (ctrl.Result, error) {
		mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(flightGK)
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

		if finalizers := flight.GetFinalizers(); flight.GetDeletionTimestamp() == nil && !slices.Contains(finalizers, "yoke.cd/atc.cleanup.flight") {
			flight.SetFinalizers(append(finalizers, "yoke.cd/atc.cleanup.flight"))
			if _, err := resourceIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: "yoke.cd/atc"}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set cleanup finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !flight.GetDeletionTimestamp().IsZero() {
			if err := yoke.FromK8Client(ctrl.Client(ctx)).Mayday(ctx, event.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to run atc cleanup: %w", err)
			}

			finalizers := flight.GetFinalizers()
			if idx := slices.Index(finalizers, "yoke.cd/atc.cleanup.flight"); idx != -1 {
				flight.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
				if _, err := resourceIntf.Update(ctx, flight, metav1.UpdateOptions{FieldManager: "yoke.cd/atc"}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
				}
			}

			return ctrl.Result{}, nil
		}

		data, err := json.Marshal(flight.Object["spec"])
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to marhshal resource: %w", err)
		}

		params := yoke.TakeoffParams{
			Release: event.Name,
			Flight: yoke.FlightParams{
				Path:                wasmPath,
				Input:               bytes.NewReader(data),
				Namespace:           event.Namespace,
				CompilationCacheDir: cacheDir,
			},
			CreateCRDs: false,
			Wait:       0,
			Poll:       0,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: flight.GetAPIVersion(),
					Kind:       flight.GetKind(),
					Name:       flight.GetName(),
					UID:        flight.GetUID(),
				},
			},
		}

		mutex.RLock()
		defer mutex.RUnlock()

		if err := yoke.FromK8Client(ctrl.Client(ctx)).Takeoff(ctx, params); err != nil {
			if !internal.IsWarning(err) {
				return ctrl.Result{}, fmt.Errorf("failed to takeoff: %w", err)
			}
			ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
		}

		return ctrl.Result{}, nil
	}

	if cleanup := atc.Cleanups[airway.GetName()]; cleanup != nil {
		cleanup()
	}

	flightCtx, cancel := context.WithCancel(ctx)

	done := make(chan struct{})

	atc.Cleanups[airway.GetName()] = func() {
		cancel()
		ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown in progress.")
		<-done
	}

	go func() {
		defer cancel()
		defer close(done)

		airwayStatus("Ready", "Flight-Controller launched")

		if err := flightController.ProcessGroupKind(flightCtx, flightGK, flightHander); err != nil {
			airwayStatus("Error", "Flight-Controller: "+err.Error())
			if errors.Is(err, context.Canceled) {
				ctrl.Logger(ctx).Info("Flight controller canceled. Shutdown complete.", "groupKind", flightGK.String())
				return
			}
			ctrl.Logger(ctx).Error("could not process group kind", "error", err)
		}
	}()

	return ctrl.Result{}, nil
}

func (atc ATC) Teardown() {
	for _, cleanup := range atc.Cleanups {
		cleanup()
	}
}

func airwaySpec(resource *unstructured.Unstructured) any {
	spec, _, _ := unstructured.NestedFieldCopy(resource.Object, "spec")
	return spec
}
