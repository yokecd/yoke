package xsync

import (
	"iter"
	"sync"
)

type Map[K comparable, V any] sync.Map

func (m *Map[K, V]) Load(key K) (V, bool) {
	result, ok := (*sync.Map)(m).Load(key)
	if !ok {
		var zero V
		return zero, ok
	}
	return result.(V), ok
}

func (m *Map[K, V]) Store(key K, value V) {
	(*sync.Map)(m).Store(key, value)
}

func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	result, ok := (*sync.Map)(m).LoadOrStore(key, value)
	return result.(V), ok
}

func (m *Map[K, V]) LoadAndDelete(key K) (V, bool) {
	result, ok := (*sync.Map)(m).LoadAndDelete(key)
	if !ok {
		var zero V
		return zero, ok
	}
	return result.(V), ok
}

func (m *Map[K, V]) Delete(key K) {
	(*sync.Map)(m).Delete(key)
}

func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		(*sync.Map)(m).Range(func(key any, value any) bool {
			return yield(key.(K), value.(V))
		})
	}
}
