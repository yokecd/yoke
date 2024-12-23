package wasm

import "sync"

type Locks struct {
	locks sync.Map
}

func (locks *Locks) Get(name string) *Lock {
	lock, _ := locks.locks.LoadOrStore(name, new(Lock))
	return lock.(*Lock)
}

func (locks *Locks) Delete(name string) {
	locks.locks.Delete(name)
}

type Lock struct {
	Flight    sync.RWMutex
	Converter sync.RWMutex
}

func (lock *Lock) LockAll() {
	lock.Flight.Lock()
	lock.Converter.Lock()
}

func (lock *Lock) UnlockAll() {
	lock.Flight.Unlock()
	lock.Converter.Unlock()
}
