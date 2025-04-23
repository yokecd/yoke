package wasm

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/yoke"
)

type Type interface {
	string() string
}

type wasmtype string

func (wasm wasmtype) string() string {
	return string(wasm)
}

var (
	Flight    wasmtype = "flight"
	Converter wasmtype = "converter"
)

func AirwayModuleDir(airwayName string) string {
	return filepath.Join("/conf", airwayName)
}

type ModuleCache struct {
	modules xsync.Map[string, *Modules]
}

func (cache *ModuleCache) Get(name string) *Modules {
	modules, _ := cache.modules.LoadOrStore(name, &Modules{
		Flight: &Module{
			Module: yoke.Module{
				Instance: &wasi.Module{},
			},
		},
		Converter: &Module{
			Module: yoke.Module{
				Instance: &wasi.Module{},
			},
		},
	})
	return modules
}

func (cache *ModuleCache) Delete(name string) {
	cache.modules.Delete(name)
}

type Modules struct {
	Flight    *Module
	Converter *Module
}

func (modules *Modules) Reset() {
	modules.Converter.Close()
	modules.Flight.Close()
}

func (modules *Modules) LockAll() {
	modules.Flight.Lock()
	modules.Converter.Lock()
}

func (modules *Modules) UnlockAll() {
	modules.Flight.Unlock()
	modules.Converter.Unlock()
}

type Module struct {
	yoke.Module
	sync.RWMutex
}

func (mod *Module) Close() {
	if mod == nil {
		return
	}
	if mod.Instance != nil {
		_ = mod.Instance.Close(context.TODO())
	}
	mod.Instance = new(wasi.Module)
}
