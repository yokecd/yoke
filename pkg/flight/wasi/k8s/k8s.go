package k8s

import (
	"encoding/json"
	"errors"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasm"

	"github.com/yokecd/yoke/pkg/flight"
	// Make sure to include wasi as it contains necessary "malloc" export that will be needed
	// for the host to allocate a wasm.Buffer. IE: any wasm module that uses this package exports wasi.malloc
	_ "github.com/yokecd/yoke/pkg/flight/wasi"
)

type ResourceIdentifier struct {
	Name       string
	Namespace  string
	Kind       string
	ApiVersion string
}

func Lookup[T any](identifier ResourceIdentifier) (*T, error) {
	var state wasm.State

	buffer := lookup(
		wasm.PtrTo(&state),
		wasm.FromString(identifier.Name),
		wasm.FromString(identifier.Namespace),
		wasm.FromString(identifier.Kind),
		wasm.FromString(identifier.ApiVersion),
	)

	switch state {
	case wasm.StateOK:
		var obj struct {
			Metadata struct {
				Labels map[string]string `json:"labels,omitempty"`
			} `json:"metadata,omitzero"`
		}
		if err := json.Unmarshal(buffer.Slice(), &obj); err != nil {
			return nil, err
		}

		labels := func() map[string]string {
			if obj.Metadata.Labels == nil {
				return map[string]string{}
			}
			return obj.Metadata.Labels
		}()

		if labels[internal.LabelYokeRelease] != flight.Release() || labels[internal.LabelYokeReleaseNS] != flight.Namespace() {
			return nil, ErrorForbidden("cannot access resource outside of target release ownership")
		}

		var resource T
		if err := json.Unmarshal(buffer.Slice(), &resource); err != nil {
			return nil, err
		}

		return &resource, nil
	case wasm.StateFeatureNotGranted:
		return nil, ErrorClusterAccessNotGranted
	case wasm.StateError:
		return nil, errors.New(buffer.String())
	case wasm.StateForbidden:
		return nil, ErrorForbidden(buffer.String())
	case wasm.StateNotFound:
		return nil, ErrorNotFound(buffer.String())
	case wasm.StateUnauthenticated:
		return nil, ErrorUnauthenticated(buffer.String())

	default:
		panic("unknown state")
	}
}
