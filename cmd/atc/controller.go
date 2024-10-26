package main

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/yokecd/yoke/internal/k8s"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
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

type HandleFunc func(Event) (Result, error)

type Controller struct {
	client      *k8s.Client
	logger      *slog.Logger
	Concurrency int
}

func (ctrl Controller) ProcessGroupKind(ctx context.Context, gk schema.GroupKind, handler HandleFunc) error {
	mapping, err := ctrl.client.Mapper.RESTMapping(gk)
	if err != nil {
		return fmt.Errorf("failed to get mapping for %s: %w", gk, err)
	}

	watcher, err := ctrl.client.Meta.Resource(mapping.Resource).Watch(context.Background(), v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch resources: %w", err)
	}
	defer watcher.Stop()

	ctrl.logger.Info("watching resources", slog.String("groupkind", gk.String()))

	events := ctrl.eventsFromWatcher(ctx, watcher)

	return ctrl.process(ctx, events, handler)
}

func (ctrl Controller) process(ctx context.Context, events chan Event, handle HandleFunc) error {
	var activeMap sync.Map

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
					if _, loaded := activeMap.LoadOrStore(event.String(), struct{}{}); loaded {
						queue.Push(event)
						return
					}

					func() {
						defer activeMap.Delete(event.String())

						logger := ctrl.logger.With(
							slog.String("event", event.String()),
							slog.Int("attempt", event.attempts),
						)

						result, err := handle(event)

						shouldRequeue := result.Requeue || result.RequeueAfter > 0 || err != nil

						if shouldRequeue && result.RequeueAfter == 0 {
							result.RequeueAfter = min(time.Duration(powInt(2, event.attempts))*time.Second, 15*time.Minute)
						}

						if shouldRequeue {
							logger = logger.With(slog.String("requeueAfter", result.RequeueAfter.String()))
							time.AfterFunc(result.RequeueAfter, func() {
								event.attempts++
								queue.Push(event)
							})
						}

						if err != nil {
							logger.Error("error processing event", slog.String("error", err.Error()))
							return
						}

						logger.Info("reconcile successfull")
						return
					}()
				}
			}
		}()
	}

	wg.Wait()

	return context.Cause(ctx)
}

func (ctrl Controller) eventsFromWatcher(ctx context.Context, watcher watch.Interface) chan Event {
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
					ctrl.logger.Warn("unexpected event type", "type", reflect.TypeOf(event.Object).String())
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
