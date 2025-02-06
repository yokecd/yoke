//go:build !wasip1

package k8s

import "github.com/yokecd/yoke/internal/wasm"

func lookup(ptr wasm.Ptr, name, namespace, kind, apiversion wasm.String) wasm.Buffer {
	panic("mock lookup not implemented: should be used in the context of wasip1")
}
