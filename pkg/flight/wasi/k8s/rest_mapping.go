//go:build !wasip1

package k8s

import "github.com/yokecd/yoke/internal/wasm"

func getRestMapping(ptr wasm.Ptr, groupOrAPIVersion, kind wasm.String) wasm.Buffer {
	panic("mock lookup not implemented: should be used in the context of wasip1")
}
