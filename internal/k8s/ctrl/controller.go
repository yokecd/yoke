package ctrl

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"reflect"
	"sync"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"

	"github.com/yokecd/yoke/internal/k8s"
)

type Event struct {
	Name      string
	Namespace string

	attempts int
}

type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

func (event Event) String() string {
	if event.Namespace == "" {
		return event.Name
	}
	return event.Namespace + "-" + event.Name
}

type HandleFunc func(context.Context, Event) (Result, error)

type Instance struct {
	Client      *k8s.Client
	Logger      *slog.Logger
	Concurrency int
}

func (ctrl Instance) ProcessGroupKind(ctx context.Context, gk schema.GroupKind, handler HandleFunc) error {
	mapping, err := ctrl.Client.Mapper.RESTMapping(gk)
	if err != nil {
		ctrl.Client.Mapper.Reset()
		mapping, err = ctrl.Client.Mapper.RESTMapping(gk)
		if err != nil {
			return fmt.Errorf("failed to get mapping for %s: %w", gk, err)
		}
	}

	logger := ctrl.Logger.With(slog.String("groupKind", gk.String()))
	logger.Info("watching resources")

	ctx = context.WithValue(ctx, loggerKey{}, logger)
	ctx = context.WithValue(ctx, rootLoggerKey{}, ctrl.Logger)

	intf := ctrl.Client.Meta.Resource(mapping.Resource)

	events := ctrl.eventsFromMetaGetter(ctx, intf, mapping)

	return ctrl.process(ctx, events, handler)
}

func (ctrl Instance) process(ctx context.Context, events chan Event, handle HandleFunc) error {
	var (
		activeMap   sync.Map
		timers      sync.Map
		concurrency = max(ctrl.Concurrency, 1)
	)

	var wg sync.WaitGroup
	wg.Add(concurrency)

	queue, stop := QueueFromChannel(events)
	defer stop()

	for range concurrency {
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				case event := <-queue.C:
					func() {
						done, loaded := activeMap.LoadOrStore(event.String(), make(chan struct{}))
						if loaded {
							wg.Add(1)
							go func() {
								defer wg.Done()
								select {
								case <-ctx.Done():
									return
								case <-done.(chan struct{}):
									queue.Enqueue(event)
								}
							}()
							return
						}
						defer close(done.(chan struct{}))
						defer activeMap.Delete(event.String())

						if timer, loaded := timers.LoadAndDelete(event.String()); loaded {
							timer.(*time.Timer).Stop()
						}

						logger := Logger(ctx).With(
							slog.String("loopId", randHex()),
							slog.String("event", event.String()),
							slog.Int("attempt", event.attempts),
						)

						// It is important that we do not cancel the handler mid-execution.
						// Rather we only exit once the loop is idle.
						ctx := context.WithoutCancel(ctx)
						ctx = context.WithValue(ctx, loggerKey{}, logger)
						ctx = context.WithValue(ctx, clientKey{}, ctrl.Client)

						logger.Info("processing event")

						start := time.Now()

						result, err := handle(ctx, event)

						shouldRequeue := result.Requeue || result.RequeueAfter > 0 || err != nil

						if shouldRequeue && result.RequeueAfter == 0 {
							result.RequeueAfter = withJitter(min(time.Duration(powInt(2, event.attempts))*time.Second, 15*time.Minute), 0.10)
						}

						if shouldRequeue {
							logger = logger.With(slog.String("requeueAfter", result.RequeueAfter.String()))
							timers.Store(event.String(), time.AfterFunc(result.RequeueAfter, func() {
								if err != nil {
									event.attempts++
								} else {
									event.attempts = 0
								}
								timers.Delete(event.String())
								queue.Enqueue(event)
							}))
						}

						if err != nil {
							logger.Error("error processing event", slog.String("error", err.Error()))
							return
						}
						logger.Info("reconcile successfull", "elapsed", time.Since(start).Round(time.Millisecond).String())
					}()
				}
			}
		}()
	}

	wg.Wait()

	return context.Cause(ctx)
}

func (ctrl Instance) eventsFromMetaGetter(ctx context.Context, getter metadata.Getter, mapping *meta.RESTMapping) chan Event {
	events := make(chan Event)
	cache := make(map[Event]*unstructured.Unstructured)
	backoff := time.Second

	setupWatcher := func() (watch.Interface, bool) {
		for {
			watcher, err := getter.Watch(context.Background(), metav1.ListOptions{})
			if err != nil {
				if kerrors.IsNotFound(err) {
					return nil, false
				}
				Logger(ctx).Error("failed to setup watcher", "error", err, "backoff", backoff.String())
				time.Sleep(backoff)
				continue
			}
			return watcher, true
		}
	}

	go func() {
		defer func() {
			Logger(ctx).Warn("watcher exited", "resource", mapping.Resource)
			close(events)
		}()

		watcher, ok := setupWatcher()
		if !ok {
			return
		}
		defer watcher.Stop()

		kubeEvents := watcher.ResultChan()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-kubeEvents:
				if !ok {
					Logger(ctx).Error("unexpected close of kube events channel")
					watcher.Stop()
					watcher, ok = setupWatcher()
					if !ok {
						return
					}
					kubeEvents = watcher.ResultChan()
					continue
				}

				if event.Type == watch.Error {
					Logger(ctx).Error("kube events sent an error", "error", event)
					continue
				}

				metadata, ok := event.Object.(*metav1.PartialObjectMetadata)
				if !ok {
					Logger(ctx).Warn("unexpected event type", "type", fmt.Sprintf("%T", event.Object), "runtimeObject", func() string {
						if event.Object == nil {
							return "<nil>"
						}
						return reflect.TypeOf(event.Object).String()
					}())
					continue
				}

				intf := func() dynamic.ResourceInterface {
					if mapping.Scope == meta.RESTScopeRoot {
						return ctrl.Client.Dynamic.Resource(mapping.Resource)
					}
					return ctrl.Client.Dynamic.Resource(mapping.Resource).Namespace(metadata.Namespace)
				}()

				evt := Event{
					Name:      metadata.Name,
					Namespace: metadata.Namespace,
				}

				if event.Type == watch.Modified || event.Type == watch.Added {
					current, err := intf.Get(ctx, metadata.Name, metav1.GetOptions{})
					if err == nil {
						prev := cache[evt]
						cache[evt] = current
						if resourcesAreEqual(prev, current) {
							continue
						}
					}
				}

				events <- evt
			}
		}
	}()

	return events
}

func powInt(base int, up int) int {
	result := 1
	for range up {
		result *= base
	}
	return result
}

func randHex() string {
	data := make([]byte, 4)
	for i := range len(data) {
		data[i] = byte(rand.UintN(256))
	}
	return hex.EncodeToString(data)
}

type loggerKey struct{}

func Logger(ctx context.Context) *slog.Logger {
	logger, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	return logger
}

type rootLoggerKey struct{}

func RootLogger(ctx context.Context) *slog.Logger {
	logger, _ := ctx.Value(rootLoggerKey{}).(*slog.Logger)
	return logger
}

type clientKey struct{}

func Client(ctx context.Context) *k8s.Client {
	client, _ := ctx.Value(clientKey{}).(*k8s.Client)
	return client
}

func withJitter(duration time.Duration, percent float64) time.Duration {
	offset := float64(duration) * percent
	jitter := 2 * offset * rand.Float64()
	return time.Duration(float64(duration) - offset + jitter).Round(time.Second)
}

func resourcesAreEqual(a, b *unstructured.Unstructured) bool {
	if (a == nil) || (b == nil) {
		return false
	}

	dropKeys := [][]string{
		{"metadata", "generation"},
		{"metadata", "resourceVersion"},
		{"metadata", "managedFields"},
		{"status"},
	}

	for _, keys := range dropKeys {
		unstructured.RemoveNestedField(a.Object, keys...)
		unstructured.RemoveNestedField(b.Object, keys...)
	}

	return reflect.DeepEqual(a.Object, b.Object)
}
