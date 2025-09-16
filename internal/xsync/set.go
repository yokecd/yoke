package xsync

import (
	"iter"
	"sync"
)

type Set[T comparable] sync.Map

func (set *Set[T]) Add(elem T) {
	(*sync.Map)(set).Store(elem, struct{}{})
}

func (set *Set[T]) Del(elem T) {
	(*sync.Map)(set).Delete(elem)
}

func (set *Set[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		(*sync.Map)(set).Range(func(key, value any) bool {
			return yield(key.(T))
		})
	}
}
