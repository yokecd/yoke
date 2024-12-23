package wasm

import "path/filepath"

type Type interface {
	string() string
}

type wasmtype string

func (wasm wasmtype) string() string {
	return string(wasm)
}

var (
	Flight    wasmtype = "flight"
	Converter wasmtype = "converter"
)

func AirwayModuleDir(airwayName string) string {
	return filepath.Join("/conf", airwayName)
}

func AirwayModulePath(airwayName string, typ Type) string {
	return filepath.Join(AirwayModuleDir(airwayName), typ.string())
}
