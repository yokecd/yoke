//go:build wasip1

package k8s

import "github.com/yokecd/yoke/internal/wasm"

//go:wasmimport host k8s_lookup
func lookup(ptr wasm.Ptr, name, namespace, kind, apiversion wasm.String) wasm.Buffer
