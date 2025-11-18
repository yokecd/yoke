package xsync

import "sync"

func OnceFunc(fn func()) func() {
	var once sync.Once
	return func() {
		once.Do(fn)
	}
}
