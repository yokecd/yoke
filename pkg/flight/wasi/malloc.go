// Package wasi exports essentials from the wasm client-side for working with wasm from the host.
// It exports a "malloc" func to let hosts allocate memory within the wasm module.
package wasi

import (
	"github.com/yokecd/yoke/internal/wasm"
)

// heap serves as a "virtualized heap" to capture memory that has been allocated via malloc.
// This way GC cannot clean up the memory that is being used.
//
// It is the responsibility of the functions that receive a Buffer malloc-ed by a host function to free it.
var heap = make(map[wasm.Buffer][]byte)

// Free releases the reference to the malloc-ed memory. Since Go does not offer manual memory management
// Free does not actively release memory but allows drops the global reference to the memory so that GC can reclaim it if necessary.
func Free(buffer wasm.Buffer) {
	delete(heap, buffer)
}

// import "github.com/yokecd/yoke/internal/wasm"

//go:wasmexport malloc
func malloc(size uint32) wasm.Buffer {
	memory := make([]byte, size)
	buffer := wasm.FromSlice(memory)
	heap[buffer] = memory
	return buffer
}
