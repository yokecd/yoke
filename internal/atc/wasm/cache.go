package wasm

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/yokecd/yoke/internal/wasi"
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
	modules sync.Map
}

func (cache *ModuleCache) Get(name string) *Modules {
	lock, _ := cache.modules.LoadOrStore(name, &Modules{
		Flight: &Module{
			Module: new(wasi.Module),
		},
		Converter: &Module{
			Module: new(wasi.Module),
		},
	})
	return lock.(*Modules)
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
	*wasi.Module
	sync.RWMutex
}

func (mod *Module) Close() {
	if mod.Module != nil {
		_ = mod.Module.Close(context.TODO())
	}
	mod.Module = new(wasi.Module)
}
