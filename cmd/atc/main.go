package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/davidmdm/x/xcontext"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
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

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	kubecfg, err := func() (kubecfg *rest.Config, err error) {
		defer func() {
			if kubecfg == nil {
				return
			}
			kubecfg.Burst = cmp.Or(kubecfg.Burst, 300)
			kubecfg.QPS = cmp.Or(kubecfg.QPS, 50)
		}()
		if cfg.KubeConfig == "" {
			return rest.InClusterConfig()
		}
		return clientcmd.BuildConfigFromFlags("", cfg.KubeConfig)
	}()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	client, err := k8s.NewClient(kubecfg)
	if err != nil {
		return fmt.Errorf("failed to instantiate kubernetes client: %w", err)
	}

	locks := new(wasm.Locks)

	var wg sync.WaitGroup
	wg.Add(2)

	defer wg.Wait()

	e := make(chan error, 2)

	go func() {
		wg.Wait()
		close(e)
	}()

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer wg.Done()

		svr := http.Server{
			Handler: Handler(locks, logger),
			Addr:    fmt.Sprintf(":%d", cfg.Port),
		}

		serverErr := make(chan error)

		go func() {
			defer close(serverErr)
			if err := svr.ListenAndServeTLS(cfg.TLS.ServerCert.Path, cfg.TLS.ServerKey.Path); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErr <- err
			}
		}()

		select {
		case err := <-serverErr:
			e <- fmt.Errorf("failed to ListenAndServeTLS: %w", err)
			return
		case <-ctx.Done():
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		logger.Info("shutting down ATC/Server")
		if err := svr.Shutdown(ctx); err != nil {
			e <- fmt.Errorf("error occured while shutting down server: %v", err)
		}

		logger.Info("ATC/Server shutdown completed successfully")
	}()

	go func() {
		defer wg.Done()

		controller := ctrl.Instance{
			Client:      client,
			Logger:      logger.With("component", "controller"),
			Concurrency: cfg.Concurrency,
		}

		airwayGK := schema.GroupKind{Kind: "Airway", Group: "yoke.cd"}

		reconciler, teardown := atc.GetReconciler(airwayGK, cfg.Service, locks, cfg.Concurrency)
		defer teardown()

		if err := controller.ProcessGroupKind(ctx, airwayGK, reconciler); err != nil {
			e <- fmt.Errorf("failed to process group kind: %s: %w", airwayGK, err)
		}
	}()

	return <-e
}
