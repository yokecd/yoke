package k8s

import (
	"encoding/json"
	"errors"

	"github.com/yokecd/yoke/internal/wasm"

	// Make sure to include wasi as it contains necessary "malloc" export that will be needed
	// for the host to allocate a wasm.Buffer. IE: any wasm module that uses this package exports wasi.malloc
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi"
)

type ResourceIdentifier struct {
	Name       string
	Namespace  string
	Kind       string
	ApiVersion string
}

type object[T any] interface {
	*T
	flight.Resource
}

func LookupResource[T any, P object[T]](value P) (P, error) {
	return Lookup[T](ResourceIdentifier{
		Name:       value.GetName(),
		Namespace:  value.GetNamespace(),
		Kind:       value.GroupVersionKind().Kind,
		ApiVersion: value.GroupVersionKind().GroupVersion().Identifier(),
	})
}

type RestMapping struct {
	Group      string
	Version    string
	Kind       string
	Resource   string
	Namespaced bool
}

func GetRestMapping(groupOrAPIVersion, kind string) (*RestMapping, error) {
	var state wasm.State

	buffer := getRestMapping(
		wasm.PtrTo(&state),
		wasm.FromString(groupOrAPIVersion),
		wasm.FromString(kind),
	)
	defer wasi.Free(buffer)

	if state != wasm.StateOK {
		return nil, errorMapping(state, buffer)
	}

	var mapping RestMapping
	if err := json.Unmarshal(buffer.Slice(), &mapping); err != nil {
		return nil, err
	}

	return &mapping, nil
}

func errorMapping(state wasm.State, buffer wasm.Buffer) error {
	switch state {
	case wasm.StateFeatureNotGranted:
		return ErrorClusterAccessNotGranted
	case wasm.StateError:
		return errors.New(buffer.String())
	case wasm.StateForbidden:
		return ErrorForbidden(buffer.String())
	case wasm.StateNotFound:
		return ErrorNotFound(buffer.String())
	case wasm.StateUnauthenticated:
		return ErrorUnauthenticated(buffer.String())
	default:
		panic("unknown state")
	}
}
