package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/davidmdm/x/xcontext"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/yoke"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		os.Exit(1)
	}
}

func run() (err error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	defer func() {
		if err != nil {
			logger.Error("program exiting with error", "error", err.Error())
		}
	}()

	ctx, stop := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	rest, err := func() (*rest.Config, error) {
		if cfg.KubeConfig == "" {
			return rest.InClusterConfig()
		}
		return clientcmd.BuildConfigFromFlags("", cfg.KubeConfig)
	}()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	client, err := k8s.NewClient(rest)
	if err != nil {
		return fmt.Errorf("failed to instantiate kubernetes client: %w", err)
	}

	go func() {
		// Listen on a port and simply return 200 too all requests. This will allow a Liveness and Readiness checks on the atc deployment.
		// TODO: make checks more sophisticated?
		http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	}()

	controller := ctrl.Instance{
		Client:      client,
		Logger:      logger,
		Concurrency: cfg.Concurrency,
	}

	airwayGK := schema.GroupKind{Kind: "Airway", Group: "yoke.cd"}

	return controller.ProcessGroupKind(ctx, airwayGK, func(ctx context.Context, event ctrl.Event) (ctrl.Result, error) {
		mapping, err := client.Mapper.RESTMapping(airwayGK)
		if err != nil {
			client.Mapper.Reset()
			return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for groupkind %s: %w", airwayGK, err)
		}

		airway, err := client.Dynamic.Resource(mapping.Resource).Get(ctx, event.Name, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				ctrl.Logger(ctx).Info("airway not found")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get airway %s: %w", event.Name, err)
		}

		if airway.GetDeletionTimestamp() != nil {
		}

		wasmURL, _, _ := unstructured.NestedString(airway.Object, "spec", "wasmUrl")

		cacheDir := filepath.Join("./cache", airway.GetName())

		wasm, err := yoke.LoadWasm(ctx, wasmURL)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to load wasm: %w", err)
		}

		if err := wasi.Compile(ctx, wasi.CompileParams{Wasm: wasm, CacheDir: cacheDir}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to compile wasm: %w", err)
		}

		wasmPath := filepath.Join(cacheDir, "source.wasm")

		if err := os.WriteFile(wasmPath, wasm, 0644); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to cache wasm asset: %w", err)
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

		if err := client.ApplyResource(ctx, crd, k8s.ApplyOpts{}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply airway's template crd: %w", err)
		}

		if err := client.WaitForReady(ctx, crd, k8s.WaitOptions{Timeout: time.Minute, Interval: time.Second}); err != nil {
			return ctrl.Result{}, fmt.Errorf("airway's template crd failed to become ready: %w", err)
		}

		go func() {
			flightController := ctrl.Instance{
				Client:      client,
				Logger:      logger,
				Concurrency: cfg.Concurrency,
			}

			group, _, _ := unstructured.NestedString(airway.Object, "spec", "template", "group")
			kind, _, _ := unstructured.NestedString(airway.Object, "spec", "template", "names", "kind")

			flightGK := schema.GroupKind{
				Group: group,
				Kind:  kind,
			}

			flightHander := func(ctx context.Context, event ctrl.Event) (ctrl.Result, error) {
				mapping, err := client.Mapper.RESTMapping(flightGK)
				if err != nil {
					client.Mapper.Reset()
					return ctrl.Result{}, fmt.Errorf("failed to get rest mapping for gk: %w", err)
				}

				resourceIntf := func() dynamic.ResourceInterface {
					if mapping.Scope == meta.RESTScopeNamespace {
						return client.Dynamic.Resource(mapping.Resource).Namespace(event.Namespace)
					}
					return client.Dynamic.Resource(mapping.Resource)
				}()

				resource, err := resourceIntf.Get(ctx, event.Name, metav1.GetOptions{})
				if err != nil {
					if kerrors.IsNotFound(err) {
						ctrl.Logger(ctx).Info("resource not found")
						return ctrl.Result{}, nil
					}
					return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
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
				}

				if err := yoke.FromK8Client(client).Takeoff(ctx, params); err != nil && !internal.IsWarning(err) {
					return ctrl.Result{}, fmt.Errorf("failed to takeoff: %w", err)
				}

				if internal.IsWarning(err) {
					ctrl.Logger(ctx).Warn("takeoff succeeded despite warnings", "warning", err)
				}

				return ctrl.Result{}, nil
			}

			if err := flightController.ProcessGroupKind(ctx, flightGK, flightHander); err != nil {
				ctrl.Logger(ctx).Error("could not process group kind", "error", err)
			}
		}()

		return ctrl.Result{}, nil
	})
}
