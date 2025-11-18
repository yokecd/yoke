package atc

import (
	"slices"

	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/xsync"
)

type _dispatcher = xsync.Map[string, *xsync.Set[ctrl.Event]]

type EventDispatcher _dispatcher

func (dispatcher *EventDispatcher) raw() *_dispatcher {
	return (*_dispatcher)(dispatcher)
}

type DispatchEvent struct {
	GK        string
	Name      string
	Namespace string
}

func (dispatcher *EventDispatcher) Dispatch(resource string) []ctrl.Event {
	mapping, loaded := dispatcher.raw().Load(resource)
	if !loaded {
		return nil
	}
	return slices.Collect(mapping.All())
}

func (dispatcher *EventDispatcher) Register(resource string, evt ctrl.Event) {
	mappings, _ := dispatcher.raw().LoadOrStore(resource, new(xsync.Set[ctrl.Event]))
	mappings.Add(evt)
}

func (dispatcher *EventDispatcher) RemoveEvent(evt ctrl.Event) {
	for _, mapping := range dispatcher.raw().All() {
		mapping.Del(evt)
	}
}
