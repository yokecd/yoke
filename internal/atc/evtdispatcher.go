package atc

import (
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/xsync"
)

type _dispatcher = xsync.Map[string, *xsync.Map[*ctrl.Instance, *xsync.Set[ctrl.Event]]]

type EventDispatcher _dispatcher

func (dispatcher *EventDispatcher) raw() *_dispatcher {
	return (*_dispatcher)(dispatcher)
}

func (dispatcher *EventDispatcher) Dispatch(resource string) {
	mapping, loaded := dispatcher.raw().Load(resource)
	if !loaded {
		return
	}
	for controller, events := range mapping.All() {
		for evt := range events.All() {
			controller.SendEvent(evt)
		}
	}
}

func (dispatcher *EventDispatcher) Register(resource string, controller *ctrl.Instance, evt ctrl.Event) {
	mapping, _ := dispatcher.raw().LoadOrStore(resource, new(xsync.Map[*ctrl.Instance, *xsync.Set[ctrl.Event]]))
	events, _ := mapping.LoadOrStore(controller, new(xsync.Set[ctrl.Event]))
	events.Add(evt)
}

func (dispatcher *EventDispatcher) RemoveEvent(controller *ctrl.Instance, evt ctrl.Event) {
	for _, mapping := range dispatcher.raw().All() {
		events, loaded := mapping.Load(controller)
		if !loaded {
			continue
		}
		events.Del(evt)
	}
}

func (dispatcher *EventDispatcher) RemoveController(controller *ctrl.Instance) {
	for _, mapping := range dispatcher.raw().All() {
		mapping.Delete(controller)
	}
}
