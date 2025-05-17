package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	kcache "k8s.io/client-go/tools/cache"
	retryWatcher "k8s.io/client-go/tools/watch"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
)

type WatchDockerConfigParams struct {
	SecretName string
	Namespace  string
	Client     *k8s.Client
	Logger     *slog.Logger
}

const keyDockerConfig = ".dockerconfigjson"

func WatchDockerConfig(ctx context.Context, params WatchDockerConfigParams) error {
	if err := os.MkdirAll(filepath.Join(home.Dir, ".docker"), 0o755); err != nil {
		return fmt.Errorf("failed to ensure docker config directory: %w", err)
	}

	targetPath := filepath.Join(home.Dir, ".docker/config.json")

	secretIntf := params.Client.Clientset.CoreV1().Secrets(params.Namespace)

	fieldSelector := "metadata.name=" + params.SecretName

	secrets, err := secretIntf.List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
	if err != nil {
		return fmt.Errorf("failed to lookup docker config secret: %w", err)
	}

	if len(secrets.Items) == 0 {
		params.Logger.Warn("no docker config found", "secretName", params.SecretName)
	}

	if len(secrets.Items) > 0 {
		configJson := secrets.Items[0].Data[keyDockerConfig]
		if len(configJson) == 0 {
			return fmt.Errorf("docker config secret found but no data found under expected key %s", keyDockerConfig)
		}
		if err := os.WriteFile(targetPath, configJson, 0o644); err != nil {
			return fmt.Errorf("failed to write docker config: %w", err)
		}
		params.Logger.Info("init: successfully setup docker credentials from secret", "secretName", params.SecretName)
	}

	watcher, err := retryWatcher.NewRetryWatcherWithContext(ctx, cmp.Or(secrets.ResourceVersion, "1"), &kcache.ListWatch{
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			return secretIntf.Watch(ctx, opts)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to watch for dockerconfig secrets: %w", err)
	}
	defer watcher.Stop()

	params.Logger.Info("watching for docker credential changes", "secretName", params.SecretName)

	events := watcher.ResultChan()

	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case evt, ok := <-events:
			if !ok {
				return fmt.Errorf("watcher exited unexpectedly")
			}

			switch evt.Type {
			case watch.Added, watch.Modified:
				if err := os.WriteFile(targetPath, evt.Object.(*corev1.Secret).Data[keyDockerConfig], 0o644); err != nil {
					params.Logger.Error("failed to write docker config", "error", err)
				}
			case watch.Deleted:
				if err := os.Remove(targetPath); err != nil {
					params.Logger.Error("failed to remove dockerconfig json", "error", err)
				}
			case watch.Error:
				params.Logger.Error("docker config secret watcher sent error", "error", evt)
			}
		}
	}
}
