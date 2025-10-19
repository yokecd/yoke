package xsync

import (
	"fmt"
	"iter"
	"slices"
	"sync"
)

type Set[T comparable] sync.Map

func (set *Set[T]) Add(elem T) {
	(*sync.Map)(set).Store(elem, struct{}{})
}

func (set *Set[T]) Has(elem T) bool {
	if set == nil {
		return false
	}
	_, ok := (*sync.Map)(set).Load(elem)
	return ok
}

func (set *Set[T]) Union(src *Set[T]) *Set[T] {
	var result Set[T]
	for item := range set.All() {
		result.Add(item)
	}
	for item := range src.All() {
		result.Add(item)
	}
	return &result
}

func (set *Set[T]) Intersection(src *Set[T]) *Set[T] {
	var result Set[T]
	for item := range set.All() {
		if src.Has(item) {
			result.Add(item)
		}
	}
	for item := range src.All() {
		if set.Has(item) {
			result.Add(item)
		}
	}
	return &result
}

func (set *Set[T]) Del(elem T) {
	(*sync.Map)(set).Delete(elem)
}

func (set *Set[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		if set == nil {
			return
		}
		(*sync.Map)(set).Range(func(key, value any) bool {
			return yield(key.(T))
		})
	}
}

func (set *Set[T]) String() string {
	return fmt.Sprint(slices.Collect(set.All()))
}
