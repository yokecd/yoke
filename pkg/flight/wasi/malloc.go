// wasi exports essentials from the wasm client-side for working with wasm from the host.
// It exports a "malloc" func to let hosts allocate memory within the wasm module.
package wasi

import "github.com/yokecd/yoke/internal/wasm"

//go:wasmexport malloc
func malloc(size uint32) wasm.Buffer {
	return wasm.FromSlice(make([]byte, size))
}
