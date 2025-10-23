package yoke

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/internal/wasm/module"
)

type ListSchematicsParams struct {
	WasmURL string
}

func ListSchematics(ctx context.Context, params ListSchematicsParams) ([]string, error) {
	wasm, err := LoadWasm(ctx, params.WasmURL, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return nil, fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	customSections := module.GetCustomSections(wasm)

	results := make([]string, len(customSections))
	for i, key := range slices.Sorted(maps.Keys(customSections)) {
		results[i] = module.StripSchematicsPrefix(key)
	}

	return results, nil
}

type GetSchematicParams struct {
	WasmURL string
	Name    string
}

func GetSchematic(ctx context.Context, params GetSchematicParams) ([]byte, error) {
	wasm, err := LoadWasm(ctx, params.WasmURL, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return nil, fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	for key, data := range module.GetCustomSections(wasm) {
		prefix, key, ok := module.CutSchematicsPrefix(key)
		if !ok || key != params.Name {
			continue
		}
		switch prefix {
		case module.PrefixSchematics:
			return data, nil
		case module.PrefixSchematicsCMD:
			var args []string
			if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&args); err != nil {
				return nil, fmt.Errorf("failed to decode schematic args: %w", err)
			}
			output, err := wasi.Execute(ctx, wasi.ExecParams{
				BinName:       "schematics",
				Args:          args,
				CompileParams: wasi.CompileParams{Wasm: wasm, HostFunctionMap: host.BuildFunctionMap(nil)},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to execute schematics: %s: %w", key, err)
			}
			return output, nil
		default:
			panic("unreachable: unknown schematic prefix: " + prefix)
		}
	}

	return nil, fmt.Errorf("schematics property not found: %s", params.Name)
}

type SetSchematicParams struct {
	WasmPath string
	Name     string
	Input    io.Reader
	CMD      bool
}

func SetSchematic(ctx context.Context, params SetSchematicParams) error {
	wasmURL, err := url.Parse(params.WasmPath)
	if err != nil {
		return fmt.Errorf("invalid wasmPath: %w", err)
	}

	if wasmURL.Scheme != "" && wasmURL.Scheme != "file" {
		return fmt.Errorf("failed to set schematics: cannot set schematics on remote wasm asset")
	}

	wasm, err := LoadWasm(ctx, params.WasmPath, false)
	if err != nil {
		return fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	data, err := io.ReadAll(params.Input)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("no data for section")
	}

	temp, err := os.CreateTemp("", "module.wasm")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}

	modified, err := func() ([]byte, error) {
		if params.CMD {
			var cmdArgs []string
			if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&cmdArgs); err != nil {
				return nil, fmt.Errorf("could not parse stdin as command args: %w", err)
			}
			return module.WithCustomSectionCMD(wasm, params.Name, cmdArgs), nil
		}
		return module.WithCustomSectionData(wasm, params.Name, data), nil
	}()
	if err != nil {
		return err
	}

	if _, err := temp.Write(modified); err != nil {
		return fmt.Errorf("failed to write to temporary location: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	if err := os.Rename(temp.Name(), params.WasmPath); err != nil {
		return fmt.Errorf("faoled to rename temporary file: %w", err)
	}

	return nil
}
