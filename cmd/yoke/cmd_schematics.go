package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yokecd/yoke/internal"
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
		{
			schematics, err := yoke.ListSchematics(ctx, yoke.ListSchematicsParams{WasmURL: wasmPath})
			if err != nil {
				return fmt.Errorf("failed to list schematics: %w", err)
			}
			for _, schematic := range schematics {
				fmt.Fprintln(internal.Stdout(ctx), schematic)
			}
			return nil
		}
	case "get":
		{
			if len(subargs) == 0 || subargs[0] == "" {
				return fmt.Errorf("name of schematics property is required")
			}
			data, err := yoke.GetSchematic(ctx, yoke.GetSchematicParams{
				WasmURL: wasmPath,
				Name:    subargs[0],
			})
			if err != nil {
				return fmt.Errorf("failed to get schematics: %w", err)
			}
			_, err = internal.Stdout(ctx).Write(data)
			return err
		}
	case "set":
		{
			setcmdFlagset := flag.NewFlagSet("schematics set", flag.ExitOnError)
			cmd := setcmdFlagset.Bool("cmd", false, "marks the input as command args to be executed to generate the schematic data")

			setcmdFlagset.Usage = func() {
				flagset.Usage()
				setcmdFlagset.PrintDefaults()
			}

			_ = setcmdFlagset.Parse(subargs)

			name := setcmdFlagset.Arg(0)
			if name == "" {
				return fmt.Errorf("name of schematics property is required")
			}

			return yoke.SetSchematic(ctx, yoke.SetSchematicParams{
				WasmPath: wasmPath,
				Name:     name,
				Input:    os.Stdin,
				CMD:      *cmd,
			})
		}
	default:
		return fmt.Errorf("unknown schematics subcommand: %q", subcmd)
	}
}
