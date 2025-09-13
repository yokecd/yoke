package xsync

import (
	"iter"
	"sync"
)

type Set[T comparable] struct {
	lock  *sync.RWMutex
	elems map[T]struct{}
}

func MakeSet[T comparable]() Set[T] {
	return Set[T]{
		lock:  new(sync.RWMutex),
		elems: make(map[T]struct{}),
	}
}

func (set Set[T]) Add(elem T) {
	set.lock.RLock()
	defer set.lock.RUnlock()
	set.elems[elem] = struct{}{}
}

func (set Set[T]) Del(elem T) {
	set.lock.RLock()
	defer set.lock.RUnlock()
	delete(set.elems, elem)
}

func (set Set[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		set.lock.Lock()
		defer set.lock.Unlock()
		for value := range set.elems {
			if !yield(value) {
				return
			}
		}
	}
}
