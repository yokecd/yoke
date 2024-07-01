package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/davidmdm/x/xcontext"
	"github.com/davidmdm/yoke/internal/k8s"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
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

			go Handle(Event{
				Type:         event.Type,
				ResourceName: metadata.Name,
			})

			fmt.Println(metadata.Name)
		}
	}
}

func Handle(event Event) (*time.Duration, error) {}

func WrapWithCanceled(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %w", context.Canceled, err)
}

type Event struct {
	Type         watch.EventType
	ResourceName string
}

type EventState struct {
	ShouldRequeue *atomic.Bool
}

type SyncMap[K, V any] sync.Map

func (m *SyncMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	val, loaded := (*sync.Map)(m).LoadAndDelete(key)
	v, _ := val.(V)
	return v, loaded
}

func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	val, loaded := (*sync.Map)(m).LoadOrStore(key, value)
	v, _ := val.(V)
	return v, loaded
}

type Worker struct {
	State       *SyncMap[Event, EventState]
	Concurrency int
	Logger      *slog.Logger
}

type HandleFunc func(Event) (requeueAfter *time.Duration, err error)

func (worker Worker) Process(events chan Event, handle HandleFunc) {
	var wg sync.WaitGroup
	wg.Add(worker.Concurrency)

	defer wg.Wait()

	for range worker.Concurrency {
		go func() {
			defer wg.Done()

			for event := range events {
				state, loaded := worker.State.LoadOrStore(event, EventState{ShouldRequeue: new(atomic.Bool)})
				if loaded {
					state.ShouldRequeue.Store(true)
					continue
				}

				requeue, err := handle(event)

				state, _ = worker.State.LoadAndDelete(event)

				if err != nil {
					if requeue == nil {
						requeue = new(time.Duration)
						*requeue = 30 * time.Second
					}
					worker.Logger.Error(
						"error processing event",
						slog.String("type", string(event.Type)),
						slog.String("resourceName", event.ResourceName),
						slog.String("error", err.Error()),
						slog.String("retryAfter", (*requeue).String()),
					)
				}

				if requeue == nil {
					if state.ShouldRequeue.Load() {
						// Do this in a separate goroutine so as to not potentially lock workers.
						// Imagine if all workers were waiting to requeue and there was no consumers left?
						// That would not be good!
						wg.Add(1)
						go func() {
							events <- event
							wg.Done()
						}()
					}
					continue
				}

				time.AfterFunc(*requeue, func() { events <- event })
			}
		}()
	}
}
