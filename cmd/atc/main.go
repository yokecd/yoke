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
	"github.com/davidmdm/x/xerr"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/xhttp"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
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
	logger := func() *slog.Logger {
		if os.Getenv("LOG_FORMAT") == "text" {
			return slog.New(slog.NewTextHandler(os.Stdout, nil))
		}
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}()
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

	client, err := k8s.NewClient(kubecfg, "")
	if err != nil {
		return fmt.Errorf("failed to instantiate kubernetes client: %w", err)
	}

	logger.Info("initializing atc")

	logger.Info("applying resources")

	teardown, err := ApplyResources(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("failed to apply dependent resources: %w", err)
	}
	defer func() {
		err = xerr.Join(err, teardown(context.Background()))
	}()

	moduleCache := cache.NewModuleCache(cfg.CacheFS, cfg.ModuleAllowList)
	eventDispatcher := new(atc.EventDispatcher)
	flightStates := &xsync.Map[string, atc.InstanceState]{}

	var wg sync.WaitGroup
	wg.Add(3)

	defer wg.Wait()

	e := make(chan error, 3)

	go func() {
		wg.Wait()
		close(e)
	}()

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer wg.Done()
		if cfg.DockerConfigSecretName == "" {
			return
		}
		logger.Info("Starting docker secret watcher")
		if err := WatchDockerConfig(ctx, WatchDockerConfigParams{
			SecretName: cfg.DockerConfigSecretName,
			Namespace:  cfg.Service.Namespace,
			Client:     client,
			Logger:     logger.With("component", "docker-watcher"),
		}); err != nil {
			e <- fmt.Errorf("error watching docker config secret: %w", err)
		}
	}()

	controller := ctrl.NewController(ctx, ctrl.Params{
		Client:      client,
		Logger:      logger.With("component", "controller"),
		Concurrency: max(cfg.Concurrency, 1),
	})
	if err := controller.RegisterGKs(map[schema.GroupKind]ctrl.Funcs{
		{Group: "yoke.cd", Kind: v1alpha1.KindAirway}:        atc.GetAirwayReconciler(cfg.Service, moduleCache, eventDispatcher, flightStates),
		{Group: "yoke.cd", Kind: v1alpha1.KindFlight}:        atc.FlightReconciler(moduleCache),
		{Group: "yoke.cd", Kind: v1alpha1.KindClusterFlight}: atc.ClusterFlightReconsiler(moduleCache),
	}); err != nil {
		return fmt.Errorf("failed to register group kind handlers: %w", err)
	}

	go func() {
		defer wg.Done()
		logger.Info("Controller Starting", "concurrency", controller.Concurrency)
		if err := controller.Run(); err != nil {
			e <- fmt.Errorf("controller exited run with error: %w", err)
		}
	}()

	go func() {
		defer wg.Done()

		filter := func() xhttp.LogFilterFunc {
			if cfg.Verbose {
				return nil
			}
			return func(pattern string, attrs []slog.Attr) bool {
				return pattern != "POST /validations/external-resources"
			}
		}()

		svr := http.Server{
			Handler: Handler(HandlerParams{
				Controller:   controller,
				FlightStates: flightStates,
				Client:       client,
				Cache:        moduleCache,
				Dispatcher:   eventDispatcher,
				Logger:       logger.With("component", "server"),
				Filter:       filter,
			}),
			Addr: fmt.Sprintf(":%d", cfg.Port),
		}

		serverErr := make(chan error, 1)

		go func() {
			defer close(serverErr)
			logger.Info("ATC Admission Control Server starting", "addr", svr.Addr)
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
			e <- fmt.Errorf("error occurred while shutting down server: %v", err)
		}

		logger.Info("ATC/Server shutdown completed successfully")
	}()

	return <-e
}
