package atc

import (
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/xsync"
)

type EventDispatcher map[*ctrl.Instance]*xsync.Map[string, xsync.Set[ctrl.Event]]

func (dispatcher EventDispatcher) Dispatch(resource string) {
	for controller, r2e := range dispatcher {
		events, ok := r2e.Load(resource)
		if !ok {
			continue
		}
		for evt := range events.All() {
			controller.SendEvent(evt)
		}
	}
}
