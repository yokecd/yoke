package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/davidmdm/x/xcontext"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	retryWatcher "k8s.io/client-go/tools/watch"

	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/home"
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

	locks := new(wasm.ModuleCache)

	var wg sync.WaitGroup
	wg.Add(2)

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

		if err := os.MkdirAll(filepath.Join(home.Dir, ".docker"), 0755); err != nil {
			e <- fmt.Errorf("failed to ensure docker config directory: %w", err)
		}

		targetPath := filepath.Join(home.Dir, ".docker/config.json")

		secretIntf := client.Clientset.CoreV1().Secrets(cfg.Service.Namespace)

		secrets, err := secretIntf.List(ctx, metav1.ListOptions{
			FieldSelector: "metadata.name=" + cfg.DockerConfigSecretName,
		})
		if err != nil {
			e <- fmt.Errorf("failed to lookup docker config secret: %w", err)
		}

		if len(secrets.Items) == 0 {
			logger.Warn("no docker config found", "secretName", cfg.DockerConfigSecretName)
		}

		if len(secrets.Items) > 0 {
			if err := os.WriteFile(targetPath, secrets.Items[0].Data[".dockerconfigjson"], 0644); err != nil {
				e <- fmt.Errorf("failed to write docker config: %w", err)
				return
			}
		}

		watcher, err := retryWatcher.NewRetryWatcher(cmp.Or(secrets.ResourceVersion, "1"), &kcache.ListWatch{
			WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
				opts.FieldSelector = "metadata.name=" + cfg.DockerConfigSecretName
				return secretIntf.Watch(ctx, opts)
			},
		})
		if err != nil {
			e <- fmt.Errorf("failed to watch for dockerconfig secrets: %w", err)
			return
		}

		// TODO: consider is this should crash the service instead of logging errors?
		for evt := range watcher.ResultChan() {
			switch evt.Type {
			case watch.Added, watch.Modified:
				if err := os.WriteFile(targetPath, evt.Object.(*corev1.Secret).Data[".dockerconfigjson"], 0644); err != nil {
					logger.Error("failed to write docker config", "error", err)
				}
			case watch.Deleted:
				if err := os.Remove(targetPath); err != nil {
					logger.Error("failed to remove dockerconfig json", "error", err)
				}
			case watch.Error:
				logger.Error("docker config secret watcher sent error", "error", evt)
			}
		}

		logger.Warn("docker config secret watcher exited unexpectedly", err)
	}()

	airwayGK := schema.GroupKind{Group: "yoke.cd", Kind: "Airway"}

	reconciler, teardown := atc.GetReconciler(airwayGK, cfg.Service, locks, cfg.Concurrency)
	defer teardown()

	controller, err := ctrl.NewController(ctx, ctrl.Params{
		GK:          airwayGK,
		Handler:     reconciler,
		Client:      client,
		Logger:      logger.With("component", "controller"),
		Concurrency: cfg.Concurrency,
	})
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	go func() {
		defer wg.Done()
		if err := controller.Run(); err != nil {
			e <- fmt.Errorf("error running the controller: %s: %w", airwayGK, err)
		}
	}()

	go func() {
		defer wg.Done()

		svr := http.Server{
			Handler: Handler(client, locks, logger.With("component", "server")),
			Addr:    fmt.Sprintf(":%d", cfg.Port),
		}

		serverErr := make(chan error, 1)

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

	return <-e
}
