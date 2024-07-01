package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"syscall"

	"github.com/davidmdm/x/xcontext"
	"github.com/davidmdm/yoke/internal/k8s"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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

	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: "yoke.sh", Kind: "Flight"})
	if err != nil {
		return fmt.Errorf("failed to get mapping for yoke/Flight: %w", err)
	}

	// Watch doesn't handle context cancellation gracefully... So we will use background and handle it ourselves...
	watcher, err := client.Meta.Resource(mapping.Resource).Watch(context.Background(), v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch resources: %w", err)
	}
	defer watcher.Stop()

	events := watcher.ResultChan()

	logger.Info("watching flights")

	for {
		select {
		case <-ctx.Done():
			return WrapWithCanceled(context.Cause(ctx))
		case event := <-events:
			metadata, ok := event.Object.(*v1.PartialObjectMetadata)
			if !ok {
				logger.Warn("unexpected event type", "type", reflect.TypeOf(event.Object).String())
				continue
			}
			fmt.Println(metadata.Name)
		}
	}
}

func WrapWithCanceled(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %w", context.Canceled, err)
}
