package wasm

import (
	"cmp"
	"context"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
)

// State is used to convey the state of a host module function call. Given that a host function
// will generally do something the wasm module cannot do, it will likely do some sort of IO.
// This means that the call can either succeed or fail with some error. This allows us to interpret
// the returned memory buffer as either containing a value or an error.
//
// State is a uint32 allowing us to define well-known generic errors that packages can use to express semantic meaning.
// It is not exhaustive. As new use cases are added, we can add new semantic errors.
//
// Currently the only host function we expose is k8s.Lookup, this means means the host function can set any of the below states
// and the k8s package can use them to return meaningful error types to the user that they can in turn act upon.
type State uint32

const (
	StateOK State = iota
	StateFeatureNotGranted
	StateError
	StateNotFound
	StateUnauthenticated
	StateForbidden
)

type Ptr uint32

func PtrTo[T any](value *T) Ptr {
	return Ptr(uintptr(unsafe.Pointer(value)))
}

func Malloc(ctx context.Context, module api.Module, data []byte) Buffer {
	results, err := module.ExportedFunction("malloc").Call(ctx, uint64(len(data)))
	if err != nil {
		panic(err)
	}
	buffer := Buffer(results[0])
	module.Memory().Write(buffer.Address(), data)
	return buffer
}

func Error(ctx context.Context, module api.Module, ptr Ptr, state State, err string) Buffer {
	mem := module.Memory()
	mem.WriteUint32Le(uint32(ptr), uint32(cmp.Or(state, StateError)))
	return Malloc(ctx, module, []byte(err))
}

type String uint64

func (value String) Load(module api.Module) string {
	return string(value.LoadBytes(module))
}

func (value String) LoadBytes(module api.Module) []byte {
	data, ok := module.Memory().Read(uint32(value>>32), uint32(value))
	if !ok {
		panic("memory read out of bounds")
	}
	return data
}

func FromString(value string) String {
	position := uint32(uintptr(unsafe.Pointer(unsafe.StringData(value))))
	bytes := uint32(len(value))
	return String(uint64(position)<<32 | uint64(bytes))
}

type Buffer uint64

func FromSlice(value []byte) Buffer {
	if len(value) == 0 {
		return 0
	}
	ptr := uint64(uintptr(unsafe.Pointer(&value[0])))
	return Buffer(ptr<<32 | uint64(len(value)))
}

func (buffer Buffer) Address() uint32 {
	return uint32(buffer >> 32)
}

func (buffer Buffer) Length() uint32 {
	return uint32(buffer)
}

func (buffer Buffer) Slice() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(buffer.Address()))), buffer.Length())
}

func (buffer Buffer) String() string {
	return unsafe.String((*byte)(unsafe.Pointer(uintptr(buffer.Address()))), buffer.Length())
}
