package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	kcache "k8s.io/client-go/tools/cache"

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

	secret, err := secretIntf.Get(ctx, params.SecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to lookup docker config secret: %w", err)
	}

	configJSON := secret.Data[keyDockerConfig]
	if len(configJSON) == 0 {
		return fmt.Errorf("docker config secret found but no data found under expected key %s", keyDockerConfig)
	}

	if err := os.WriteFile(targetPath, configJSON, 0o644); err != nil {
		return fmt.Errorf("failed to write docker config: %w", err)
	}

	params.Logger.Info("successfully setup docker credentials from secret", "secretName", params.SecretName)

	factory := informers.NewSharedInformerFactoryWithOptions(
		params.Client.Clientset,
		0,
		informers.WithNamespace(params.Namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", params.SecretName).String()
		}),
	)

	secretInformer := factory.Core().V1().Secrets()

	writeFunc := func(secret *corev1.Secret) {
		configJSON := secret.Data[keyDockerConfig]
		if data, err := os.ReadFile(targetPath); err == nil && bytes.Equal(configJSON, data) {
			return
		}
		if len(configJSON) == 0 {
			params.Logger.Error("empty config in secret")
			return
		}
		if err := os.WriteFile(targetPath, configJSON, 0o644); err != nil {
			params.Logger.Error("failed to write docker config", "error", err)
			return
		}
		params.Logger.Info("docker config written")
	}

	secretInformer.Informer().AddEventHandler(
		kcache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { writeFunc(obj.(*corev1.Secret)) },
			UpdateFunc: func(_ any, obj any) { writeFunc(obj.(*corev1.Secret)) },
			DeleteFunc: func(obj any) {
				if err := os.Remove(targetPath); err != nil {
					params.Logger.Error("failed to remove docker config", "error", err)
					return
				}
				params.Logger.Info("removed docker config")
			},
		},
	)

	factory.StartWithContext(ctx)

	<-ctx.Done()

	return context.Cause(ctx)
}
