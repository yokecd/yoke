//go:build wasip1

package k8s

import "github.com/yokecd/yoke/internal/wasm"

//go:wasmimport host k8s_rest_mapping
func getRestMapping(ptr wasm.Ptr, groupOrAPIVersion, kind wasm.String) wasm.Buffer
