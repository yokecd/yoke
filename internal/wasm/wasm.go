package wasm

import (
	"unsafe"
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

type String uint64

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

// Slice returns a copy of the data from the underlying Buffer by reading the data at the buffer address for its length.
// Once read, the buffer is safe to be freed.
func (buffer Buffer) Slice() []byte {
	return append([]byte{}, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(buffer.Address()))), buffer.Length())...)
}

func (buffer Buffer) String() string {
	return string(buffer.Slice())
}
