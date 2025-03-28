package xsync

import "sync"

type Map[K comparable, V any] sync.Map

func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	result, ok := (*sync.Map)(m).LoadOrStore(key, value)
	return result.(V), ok
}

func (m *Map[K, V]) Delete(key K) {
	(*sync.Map)(m).Delete(key)
}
