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

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/davidmdm/x/xcontext"
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

	rest.Burst = cmp.Or(rest.Burst, 300)
	rest.QPS = cmp.Or(rest.QPS, 50)

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

	atc := ATC{
		Airway:      schema.GroupKind{Group: "yoke.cd", Kind: "Airway"},
		Concurrency: cfg.Concurrency,
		Cleanups:    map[string]func(){},
		Locks:       &sync.Map{},
		Prev:        map[string]any{},
	}

	defer atc.Teardown()

	return controller.ProcessGroupKind(ctx, atc.Airway, atc.Reconcile)
}
