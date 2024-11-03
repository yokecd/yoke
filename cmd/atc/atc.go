package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/yoke"
)

type ATC struct {
	Airway      schema.GroupKind
	Concurrency int
	Cleanups    map[string]func()
	Locks       *sync.Map
}

func (atc ATC) Reconcile(ctx context.Context, event ctrl.Event) (ctrl.Result, error) {
	mapping, err := ctrl.Client(ctx).Mapper.RESTMapping(atc.Airway)
	if err != nil {
		ctrl.Client(ctx).Mapper.Reset()
		return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for groupkind %s: %w", atc.Airway, err)
	}

	airway, err := ctrl.Client(ctx).Dynamic.Resource(mapping.Resource).Get(ctx, event.Name, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			ctrl.Logger(ctx).Info("airway not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get airway %s: %w", event.Name, err)
	}

	wasmURL, _, _ := unstructured.NestedString(airway.Object, "spec", "wasmUrl")

	cacheDir := filepath.Join("./cache", airway.GetName())

	wasm, err := yoke.LoadWasm(ctx, wasmURL)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to load wasm: %w", err)
	}

	mutex := func() *sync.RWMutex {
		value, _ := atc.Locks.LoadOrStore(airway.GetName(), new(sync.RWMutex))
		return value.(*sync.RWMutex)
	}()

	wasmPath := filepath.Join(cacheDir, "source.wasm")

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
		Concurrency: max(atc.Concurrency, 1),
	}

	group, _, _ := unstructured.NestedString(airway.Object, "spec", "template", "group")
	kind, _, _ := unstructured.NestedString(airway.Object, "spec", "template", "names", "kind")

	flightGK := schema.GroupKind{
		Group: group,
		Kind:  kind,
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

		resource, err := resourceIntf.Get(ctx, event.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				ctrl.Logger(ctx).Info("resource not found")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
		}

		if finalizers := resource.GetFinalizers(); resource.GetDeletionTimestamp() == nil && !slices.Contains(finalizers, "yoke.cd/atc.cleanup.flight") {
			resource.SetFinalizers(append(finalizers, "yoke.cd/atc.cleanup.flight"))
			if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: "yoke.cd/atc"}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set cleanup finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}

		if !resource.GetDeletionTimestamp().IsZero() {

			if err := yoke.FromK8Client(ctrl.Client(ctx)).Mayday(ctx, event.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to run atc cleanup: %w", err)
			}

			finalizers := resource.GetFinalizers()
			if idx := slices.Index(finalizers, "yoke.cd/atc.cleanup.flight"); idx != -1 {
				resource.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
				if _, err := resourceIntf.Update(ctx, resource, metav1.UpdateOptions{FieldManager: "yoke.cd/atc"}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
				}
			}

			return ctrl.Result{}, nil
		}

		data, err := json.Marshal(resource.Object["spec"])
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
					APIVersion: resource.GetAPIVersion(),
					Kind:       resource.GetKind(),
					Name:       resource.GetName(),
					UID:        resource.GetUID(),
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

		if err := flightController.ProcessGroupKind(flightCtx, flightGK, flightHander); err != nil {
			if errors.Is(err, context.Canceled) {
				ctrl.Logger(ctx).Info("Flight controller cancled. Shutdown complete.", "groupKind", flightGK.String())
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
