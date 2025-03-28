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

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/x/xruntime"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	kcache "k8s.io/client-go/tools/cache"
	retryWatcher "k8s.io/client-go/tools/watch"

	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/xsync"
)

type Event struct {
	Name      string
	Namespace string

	attempts int
	typ      string
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
	ctx    context.Context
	events chan Event
	Params
}

type Params struct {
	GK          schema.GroupKind
	Handler     HandleFunc
	Client      *k8s.Client
	Logger      *slog.Logger
	Concurrency int
}

func NewController(ctx context.Context, params Params) (Instance, error) {
	params.Client.Mapper.Reset()

	mapping, err := params.Client.Mapper.RESTMapping(params.GK)
	if err != nil {
		return Instance{}, fmt.Errorf("failed to get mapping for %s: %w", params.GK, err)
	}

	logger := params.Logger.With(slog.String("groupKind", params.GK.String()))
	logger.Info("watching resources")

	ctx = context.WithValue(ctx, loggerKey{}, logger)
	ctx = context.WithValue(ctx, rootLoggerKey{}, params.Logger)

	params.Handler = safe(params.Handler)

	instance := Instance{
		ctx:    ctx,
		events: make(chan Event),
		Params: params,
	}

	intf := params.Client.Meta.Resource(mapping.Resource)

	instance.events, err = instance.eventsFromMetaGetter(ctx, intf, mapping)
	if err != nil {
		return instance, fmt.Errorf("failed to setup event stream: %w", err)
	}

	return instance, nil
}

func (ctrl Instance) Run() error {
	var (
		activeMap   xsync.Map[string, chan struct{}]
		timers      sync.Map
		concurrency = max(ctrl.Concurrency, 1)
	)

	var wg sync.WaitGroup
	wg.Add(concurrency)

	queue, stop := QueueFromChannel(ctrl.events)
	defer stop()

	for range concurrency {
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctrl.ctx.Done():
					return
				case event := <-queue.C:
					func() {
						defer func() {
							if e := recover(); e != nil {
								Logger(ctrl.ctx).Error("Caught Control Loop Panic", "error", e, "stack", xruntime.CallStack(-1))
							}
						}()

						done, loaded := activeMap.LoadOrStore(event.String(), make(chan struct{}))
						if loaded {
							wg.Add(1)
							go func() {
								defer wg.Done()
								select {
								case <-ctrl.ctx.Done():
									return
								case <-done:
									queue.Enqueue(event)
								}
							}()
							return
						}
						defer close(done)
						defer activeMap.Delete(event.String())

						if timer, loaded := timers.LoadAndDelete(event.String()); loaded {
							timer.(*time.Timer).Stop()
						}

						logger := Logger(ctrl.ctx).With(
							slog.String("loopId", randHex()),
							slog.Group(
								"event",
								"name", event.String(),
								"attempt", event.attempts,
								"type", event.typ,
							),
						)

						// It is important that we do not cancel the handler mid-execution.
						// Rather we only exit once the loop is idle.
						ctx := context.WithoutCancel(ctrl.ctx)
						ctx = context.WithValue(ctx, loggerKey{}, logger)
						ctx = context.WithValue(ctx, clientKey{}, ctrl.Client)

						logger.Info("processing event")

						start := time.Now()

						result, err := ctrl.Handler(ctx, event)

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

	return context.Cause(ctrl.ctx)
}

func (ctrl Instance) eventsFromMetaGetter(ctx context.Context, getter metadata.Getter, mapping *meta.RESTMapping) (chan Event, error) {
	events := make(chan Event)
	cache := make(map[Event]*unstructured.Unstructured)

	list, err := getter.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	watcher, err := retryWatcher.NewRetryWatcher(list.ResourceVersion, &kcache.ListWatch{WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
		return getter.Watch(ctx, options)
	}})
	if err != nil {
		return nil, fmt.Errorf("failed to initiate watch: %w", err)
	}

	go func() {
		defer func() {
			Logger(ctx).Warn("watcher exited", "resource", mapping.Resource)
			close(events)
		}()

		kubeEvents := watcher.ResultChan()
		defer watcher.Stop()

		for _, item := range list.Items {
			events <- Event{
				Name:      item.Name,
				Namespace: item.Namespace,
				typ:       "start-up",
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-kubeEvents:
				if !ok {
					Logger(ctx).Error("unexpected close of kube events channel")
					return
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
					typ:       string(event.Type),
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

	return events, nil
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

func safe(handler HandleFunc) HandleFunc {
	return func(ctx context.Context, event Event) (result Result, err error) {
		defer func() {
			if e := recover(); e != nil {
				err = xerr.MultiErrFrom("", err, fmt.Errorf("%v", e))
				Logger(ctx).Error("Caught Panic", "error", err, "stack", xruntime.CallStack(-1).String())
			}
		}()
		return handler(ctx, event)
	}
}
