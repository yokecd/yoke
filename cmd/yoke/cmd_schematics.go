package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/internal/wasm/module"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_schematics_help.txt
var schematicsHelp string

func init() {
	schematicsHelp = strings.TrimSpace(internal.Colorize(schematicsHelp))
}

func SchematicsCommand(ctx context.Context, args []string) error {
	flagset := flag.NewFlagSet("schematics", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), schematicsHelp)
		flagset.PrintDefaults()
	}

	var wasmPath string
	flagset.StringVar(&wasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")

	flagset.Parse(args)

	if wasmPath == "" {
		flagset.Usage()
		return fmt.Errorf("--wasm is required")
	}

	if len(flagset.Args()) == 0 {
		flagset.Usage()
		return fmt.Errorf("no subcommand given to schematics")
	}

	subcmd, subargs := flagset.Arg(0), flagset.Args()[1:]

	switch subcmd {
	case "ls":
		return ListSchematicsCommand(ctx, wasmPath)
	case "get":
		return GetSchematicsCMD(ctx, wasmPath, subargs)
	case "set":
		return SetSchematicsCMD(ctx, flagset.Usage, wasmPath, subargs)
	default:
		return fmt.Errorf("unknown schematics subcommand: %q", subcmd)
	}
}

func GetSchematicsCMD(ctx context.Context, wasmURL string, args []string) error {
	wasm, err := yoke.LoadWasm(ctx, wasmURL, false)
	if err != nil {
		return fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("name of schematics property is required")
	}

	name := args[0]

	for key, data := range module.GetCustomSections(wasm) {
		prefix, key, ok := module.CutSchematicsPrefix(key)
		if !ok || key != name {
			continue
		}
		switch prefix {
		case module.PrefixSchematics:
			_, _ = internal.Stdout(ctx).Write(data)
		case module.PrefixSchematicsCMD:
			var args []string
			if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&args); err != nil {
				return fmt.Errorf("failed to decode schematic args: %w", err)
			}
			output, err := wasi.Execute(ctx, wasi.ExecParams{
				BinName:       "schematics",
				Args:          args,
				CompileParams: wasi.CompileParams{Wasm: wasm, HostFunctionMap: host.BuildFunctionMap(nil)},
			})
			if err != nil {
				return fmt.Errorf("failed to execute schematics: %s: %w", key, err)
			}
			_, _ = internal.Stdout(ctx).Write(output)
		}
		return nil
	}

	return fmt.Errorf("schematics property not found: %s", name)
}

func ListSchematicsCommand(ctx context.Context, wasmURL string) error {
	wasm, err := yoke.LoadWasm(ctx, wasmURL, false)
	if err != nil {
		return fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	for _, key := range slices.Sorted(maps.Keys(module.GetCustomSections(wasm))) {
		fmt.Fprintln(internal.Stdout(ctx), module.StripSchematicsPrefix(key))
	}
	return nil
}

func SetSchematicsCMD(ctx context.Context, usage func(), wasmPath string, args []string) error {
	flagset := flag.NewFlagSet("schematics set", flag.ExitOnError)
	cmd := flagset.Bool("cmd", false, "marks the input as command args to be executed to generate the schematic data")
	flagset.Usage = func() {
		usage()
		flagset.PrintDefaults()
	}
	flagset.Parse(args)

	name := flagset.Arg(0)
	if name == "" {
		return fmt.Errorf("name of schematics property is required")
	}

	wasmURL, err := url.Parse(wasmPath)
	if err != nil {
		return fmt.Errorf("invalid wasmPath: %w", err)
	}
	if wasmURL.Scheme != "" && wasmURL.Scheme != "file" {
		return fmt.Errorf("failed to set schematics: cannot set schematics on remote wasm asset")
	}

	wasm, err := yoke.LoadWasm(ctx, wasmPath, false)
	if err != nil {
		return fmt.Errorf("failed to load wasm: %w", err)
	}

	if err := module.ValidatePreamble(wasm); err != nil {
		return fmt.Errorf("invalid wasm module: failed to validate preamble: %w", err)
	}

	data, err := io.ReadAll(os.Stdin)
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
		if *cmd {
			var cmdArgs []string
			if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&cmdArgs); err != nil {
				return nil, fmt.Errorf("could not parse stdin as command args: %w", err)
			}
			return module.WithCustomSectionCMD(wasm, name, cmdArgs), nil
		}
		return module.WithCustomSectionData(wasm, name, data), nil
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

	if err := os.Rename(temp.Name(), wasmPath); err != nil {
		return fmt.Errorf("faoled to rename temporary file: %w", err)
	}

	return nil
}
