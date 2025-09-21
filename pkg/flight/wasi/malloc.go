// wasi exports essentials from the wasm client-side for working with wasm from the host.
// It exports a "malloc" func to let hosts allocate memory within the wasm module.
package wasi

import (
	"github.com/yokecd/yoke/internal/wasm"
)

// global serves to capture memory that has been allocated via malloc.
// This way GC cannot clean up the memory that is being used.
//
// TODO: If we want clients to be long lived, we may want to free memory.
// Host -> mallocs
// Guest -> Frees
var global [][]byte

//go:wasmexport malloc
func malloc(size uint32) wasm.Buffer {
	memory := make([]byte, size)
	global = append(global, memory)
	buffer := wasm.FromSlice(memory)
	return buffer
}
