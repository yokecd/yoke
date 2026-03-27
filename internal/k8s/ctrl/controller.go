package ctrl

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/x/xruntime"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kcache "k8s.io/client-go/tools/cache"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/xsync"
)

type eventMeta struct {
	attempts int
}
type Event struct {
	Name      string
	Namespace string
	schema.GroupKind

	meta eventMeta
}

func (evt Event) WithoutMeta() Event {
	return Event{
		Name:      evt.Name,
		Namespace: evt.Namespace,
		GroupKind: evt.GroupKind,
	}
}

type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

func (evt Event) String() string {
	return fmt.Sprintf("%s/%s:%s", evt.Namespace, evt.GroupKind, evt.Name)
}

type HandleFunc func(context.Context, Event) (Result, error)

type gkstate struct {
	handler  HandleFunc
	shutdown func()
}

type Instance struct {
	ctx    context.Context
	events *Queue[Event]
	gks    xsync.Map[schema.GroupKind, gkstate]
	Params
}

type Funcs struct {
	Handler  HandleFunc
	Teardown func()
}

type Params struct {
	Client      *k8s.Client
	Logger      *slog.Logger
	Concurrency int
}

func NewController(ctx context.Context, params Params) *Instance {
	ctx = context.WithValue(ctx, loggerKey{}, params.Logger)
	ctx = context.WithValue(ctx, rootLoggerKey{}, params.Logger)

	return &Instance{
		ctx:    ctx,
		Params: params,
		events: NewQueue[Event](),
		gks:    xsync.Map[schema.GroupKind, gkstate]{},
	}
}

type Entry struct {
	GroupKind  schema.GroupKind
	Forwarders []schema.GroupKind
	Funcs      Funcs
}

func (instance *Instance) Register(entries ...Entry) error {
	var errs []error
	for _, entry := range entries {
		if err := instance.register(entry); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.GroupKind, err))
		}
	}
	return xerr.JoinOrdered(errs...)
}

func (instance *Instance) register(entry Entry) error {
	instance.Client.Mapper.Reset()

	mapping, err := instance.Client.Mapper.RESTMapping(entry.GroupKind)
	if err != nil {
		return fmt.Errorf("failed to get rest mapping: %w", err)
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(instance.Client.Dynamic, 0)

	informer := factory.ForResource(mapping.Resource).Informer()

	var resourceMap xsync.Set[Event]

	informerHandler := func(op func(Event)) func(obj any) {
		return func(obj any) {
			resource := obj.(*unstructured.Unstructured)
			event := Event{
				Name:      resource.GetName(),
				Namespace: resource.GetNamespace(),
				GroupKind: entry.GroupKind,
			}
			instance.events.Enqueue(event)
			op(event)
		}
	}

	informerUpdateHandler := func(oldObj any, newObj any) {
		prev := oldObj.(*unstructured.Unstructured)
		next := newObj.(*unstructured.Unstructured)
		if internal.ResourcesAreEqual(prev, next) {
			return
		}
		event := Event{
			Name:      next.GetName(),
			Namespace: next.GetNamespace(),
			GroupKind: entry.GroupKind,
		}
		instance.events.Enqueue(event)
		resourceMap.Add(event)
	}

	eventHandlers := kcache.ResourceEventHandlerFuncs{
		AddFunc:    informerHandler(resourceMap.Add),
		DeleteFunc: informerHandler(resourceMap.Del),
		UpdateFunc: informerUpdateHandler,
	}

	if _, err := informer.AddEventHandler(eventHandlers); err != nil {
		return fmt.Errorf("failed to add event handlers: %w", err)
	}

	for _, forward := range entry.Forwarders {
		mapping, err := instance.Client.Mapper.RESTMapping(forward)
		if err != nil {
			return fmt.Errorf("failed to get rest mapping: %w", err)
		}

		requeue := func(obj any) {
			resource := obj.(*unstructured.Unstructured)
			evt := Event{
				Name:      resource.GetName(),
				Namespace: resource.GetNamespace(),
				GroupKind: entry.GroupKind,
			}
			if resourceMap.Has(evt) {
				instance.events.Enqueue(evt)
			}
		}

		if _, err := factory.ForResource(mapping.Resource).Informer().AddEventHandler(kcache.ResourceEventHandlerFuncs{
			AddFunc:    requeue,
			UpdateFunc: func(_ any, obj any) { requeue(obj) },
			DeleteFunc: requeue,
		}); err != nil {
			return fmt.Errorf("failed to add event handlers for forwarder: %s: %w", forward, err)
		}
	}

	done := make(chan struct{})

	factory.Start(done)

	instance.gks.Store(
		entry.GroupKind,
		gkstate{
			handler: entry.Funcs.Handler,
			shutdown: xsync.OnceFunc(func() {
				close(done)
				factory.Shutdown()
				instance.gks.Delete(entry.GroupKind)
				entry.Funcs.Teardown()
			}),
		},
	)

	return nil
}

func (instance *Instance) ShutdownGK(gk schema.GroupKind) {
	state, ok := instance.gks.Load(gk)
	if !ok {
		return
	}
	state.shutdown()
}

func (instance *Instance) Run() error {
	defer instance.events.Stop()

	var (
		activeMap   xsync.Map[string, chan struct{}]
		timers      xsync.Map[string, *time.Timer]
		concurrency = max(instance.Concurrency, 1)
	)

	var wg sync.WaitGroup

	for range concurrency {
		wg.Go(func() {
			for {
				select {
				case <-instance.ctx.Done():
					return
				case event := <-instance.events.C:
					func() {
						defer func() {
							if e := recover(); e != nil {
								Logger(instance.ctx).Error("Caught Control Loop Panic", "error", e, "stack", xruntime.CallStack(-1))
							}
						}()

						state, ok := instance.gks.Load(event.GroupKind)
						if !ok {
							Logger(instance.ctx).Warn("event received but not handler registered for groupkind", "gk", event.GroupKind)
							return
						}

						done, loaded := activeMap.LoadOrStore(event.String(), make(chan struct{}))
						if loaded {
							wg.Go(func() {
								select {
								case <-instance.ctx.Done():
									return
								case <-done:
									instance.events.Enqueue(event)
								}
							})
							return
						}
						defer close(done)
						defer activeMap.Delete(event.String())

						if timer, loaded := timers.LoadAndDelete(event.String()); loaded {
							timer.Stop()
						}

						logger := Logger(instance.ctx).With(
							slog.String("loopId", randHex()),
							slog.Group(
								"event",
								"name", event.Name,
								"namespace", event.Namespace,
								"groupKind", event.GroupKind,
								"attempt", event.meta.attempts,
							),
						)

						// It is important that we do not cancel the handler mid-execution.
						// Rather we only exit once the loop is idle.
						ctx := context.WithoutCancel(instance.ctx)
						ctx = context.WithValue(ctx, loggerKey{}, logger)
						ctx = context.WithValue(ctx, clientKey{}, instance.Client)
						ctx = context.WithValue(ctx, instanceKey{}, instance)
						ctx = internal.WithStdio(ctx, io.Discard, io.Discard, internal.Stdin(ctx))

						logger.Info("processing event")

						start := time.Now()

						result, err := safe(state.handler)(ctx, event)

						shouldRequeue := result.Requeue || result.RequeueAfter > 0 || err != nil

						if shouldRequeue && result.RequeueAfter == 0 {
							result.RequeueAfter = withJitter(min(time.Duration(powInt(2, event.meta.attempts))*time.Second, 15*time.Minute), 0.10)
						}

						if shouldRequeue {
							logger = logger.With(slog.String("requeueAfter", result.RequeueAfter.String()))
							timers.Store(event.String(), time.AfterFunc(result.RequeueAfter, func() {
								if err != nil {
									event.meta.attempts++
								} else {
									event.meta.attempts = 0
								}
								timers.Delete(event.String())
								instance.events.Enqueue(event)
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
		})
	}

	wg.Wait()

	return context.Cause(instance.ctx)
}

func (instance *Instance) IsListeningForGK(gk schema.GroupKind) bool {
	_, ok := instance.gks.Load(gk)
	return ok
}

func (instance *Instance) SendEvent(evt Event) {
	instance.events.Enqueue(evt)
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

type instanceKey struct{}

func Inst(ctx context.Context) *Instance {
	instance, _ := ctx.Value(instanceKey{}).(*Instance)
	return instance
}

func withJitter(duration time.Duration, percent float64) time.Duration {
	offset := float64(duration) * percent
	jitter := 2 * offset * rand.Float64()
	return time.Duration(float64(duration) - offset + jitter).Round(time.Second)
}

func safe(handler HandleFunc) HandleFunc {
	return func(ctx context.Context, event Event) (result Result, err error) {
		defer func() {
			if e := recover(); e != nil {
				err = xerr.Join(err, fmt.Errorf("%v", e))
				Logger(ctx).Error("Caught Panic", "error", err, "stack", xruntime.CallStack(-1).String())
			}
		}()
		return handler(ctx, event)
	}
}
