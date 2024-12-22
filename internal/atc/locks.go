package atc

import "sync"

type WasmLocks struct {
	locks sync.Map
}

func (locks *WasmLocks) Get(name string) *WasmLock {
	lock, _ := locks.locks.LoadOrStore(name, new(WasmLock))
	return lock.(*WasmLock)
}

func (locks *WasmLocks) Delete(name string) {
	locks.locks.Delete(name)
}

type WasmLock struct {
	Flight    sync.RWMutex
	Converter sync.RWMutex
}

func (lock *WasmLock) LockAll() {
	lock.Flight.Lock()
	lock.Converter.Lock()
}

func (lock *WasmLock) UnlockAll() {
	lock.Flight.Unlock()
	lock.Converter.Unlock()
}
