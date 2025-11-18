package atc

import (
	"sync"

	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
)

const (
	fieldManager           = "yoke.cd/atc"
	cleanupFinalizer       = "yoke.cd/mayday.flight"
	cleanupAirwayFinalizer = "yoke.cd/strip.airway"
	LabelInstanceGroupKind = "instance.atc.yoke.cd/groupKind"
	LabelInstanceName      = "instance.atc.yoke.cd/name"
	LabelInstanceNamespace = "instance.atc.yoke.cd/namespace"
)

type InstanceState struct {
	Mode             v1alpha1.AirwayMode
	Mutex            *sync.RWMutex
	ClusterAccess    bool
	TrackedResources *xsync.Set[string]
}

func GetAirwayReconciler(service ServiceDef, cache *wasm.ModuleCache, dispatcher *EventDispatcher, states *xsync.Map[string, InstanceState]) ctrl.Funcs {
	atc := atc{
		service:      service,
		cleanups:     map[string]func(){},
		moduleCache:  cache,
		dispatcher:   dispatcher,
		flightStates: states,
	}
	return ctrl.Funcs{
		Handler:  atc.Reconcile,
		Teardown: atc.Teardown,
	}
}

type atc struct {
	dispatcher   *EventDispatcher
	flightStates *xsync.Map[string, InstanceState]
	service      ServiceDef
	cleanups     map[string]func()
	moduleCache  *wasm.ModuleCache
}

func (atc atc) Teardown() {
	for _, cleanup := range atc.cleanups {
		cleanup()
	}
}

type ServiceDef struct {
	Name      string
	Namespace string
	CABundle  []byte
	Port      int32
}
