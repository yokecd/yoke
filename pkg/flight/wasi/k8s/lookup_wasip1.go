//go:build wasip1

package k8s

import (
	"encoding/json"

	"github.com/yokecd/yoke/internal/wasm"
	"github.com/yokecd/yoke/pkg/flight/wasi"
)

//go:wasmimport host k8s_lookup
func lookup(ptr wasm.Ptr, name, namespace, kind, apiversion wasm.String) wasm.Buffer

func Lookup[T any](identifier ResourceIdentifier) (*T, error) {
	var state wasm.State

	buffer := lookup(
		wasm.PtrTo(&state),
		wasm.FromString(identifier.Name),
		wasm.FromString(identifier.Namespace),
		wasm.FromString(identifier.Kind),
		wasm.FromString(identifier.ApiVersion),
	)
	defer wasi.Free(buffer)

	if state != wasm.StateOK {
		return nil, errorMapping(state, buffer)
	}

	var resource T
	if err := json.Unmarshal(buffer.Slice(), &resource); err != nil {
		return nil, err
	}

	return &resource, nil
}
