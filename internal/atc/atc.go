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

type Controller struct {
	*ctrl.Instance
	values *xsync.Map[string, InstanceState]
}

func (controller Controller) FlightState(name, ns string) (InstanceState, bool) {
	state, ok := controller.values.Load(ctrl.Event{Name: name, Namespace: ns}.String())
	return state, ok
}

type ControllerCache = xsync.Map[string, Controller]

func GetAirwayReconciler(service ServiceDef, cache *wasm.ModuleCache, controllers *ControllerCache, dispatcher *EventDispatcher, concurrency int) (ctrl.HandleFunc, func()) {
	atc := atc{
		concurrency: concurrency,
		service:     service,
		cleanups:    map[string]func(){},
		moduleCache: cache,
		controllers: controllers,
		dispatcher:  dispatcher,
	}
	return atc.Reconcile, atc.Teardown
}

type atc struct {
	concurrency int

	dispatcher  *EventDispatcher
	controllers *ControllerCache
	service     ServiceDef
	cleanups    map[string]func()
	moduleCache *wasm.ModuleCache
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
