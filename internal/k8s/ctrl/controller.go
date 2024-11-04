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

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

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
	return event.Namespace + "/" + event.Name
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

	watcher, err := ctrl.Client.Meta.Resource(mapping.Resource).Watch(context.Background(), v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch resources: %w", err)
	}
	defer watcher.Stop()

	logger := ctrl.Logger.With(slog.String("groupKind", gk.String()))
	logger.Info("watching resources")

	ctx = context.WithValue(ctx, loggerKey{}, logger)
	ctx = context.WithValue(ctx, rootLoggerKey{}, ctrl.Logger)

	events := ctrl.eventsFromWatcher(ctx, watcher)

	return ctrl.process(ctx, events, handler)
}

func (ctrl Instance) process(ctx context.Context, events chan Event, handle HandleFunc) error {
	var activeMap sync.Map
	var timers sync.Map

	var wg sync.WaitGroup
	wg.Add(ctrl.Concurrency)

	queue := QueueFromChannel(events, ctrl.Concurrency)
	queueCh := queue.C()

	for range ctrl.Concurrency {
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				case event := <-queueCh:
					func() {
						done, loaded := activeMap.LoadOrStore(event.String(), make(chan struct{}))
						if loaded {
							go func() {
								<-done.(chan struct{})
								queue.Enqueue(event)
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

						result, err := handle(ctx, event)

						shouldRequeue := result.Requeue || result.RequeueAfter > 0 || err != nil

						if shouldRequeue && result.RequeueAfter == 0 {
							result.RequeueAfter = min(time.Duration(powInt(2, event.attempts))*time.Second, 15*time.Minute)
						}

						if shouldRequeue {
							logger = logger.With(slog.String("requeueAfter", result.RequeueAfter.String()))
							timers.Store(event.String(), time.AfterFunc(result.RequeueAfter, func() {
								event.attempts++
								timers.Delete(event.String())
								queue.Enqueue(event)
							}))
						}

						if err != nil {
							logger.Error("error processing event", slog.String("error", err.Error()))
							return
						}
						logger.Info("reconcile successfull")
					}()
				}
			}
		}()
	}

	wg.Wait()

	return context.Cause(ctx)
}

func (ctrl Instance) eventsFromWatcher(ctx context.Context, watcher watch.Interface) chan Event {
	events := make(chan Event)

	go func() {
		kubeEvents := watcher.ResultChan()
		defer watcher.Stop()

		for {
			select {
			case <-ctx.Done():
				close(events)
				return
			case event := <-kubeEvents:
				metadata, ok := event.Object.(*v1.PartialObjectMetadata)
				if !ok {
					ctrl.Logger.Warn("unexpected event type", "type", reflect.TypeOf(event.Type), "runtimeObject", func() string {
						if event.Object == nil {
							return "<nil>"
						}
						return reflect.TypeOf(event.Object).String()
					}())
					continue
				}

				events <- Event{
					Name:      metadata.Name,
					Namespace: metadata.Namespace,
				}
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
