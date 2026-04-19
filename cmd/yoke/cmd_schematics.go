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

var schematicsWasmPath string
var CmdSchematics = &YokeCommand{
	Name:    "schematics",
	Aliases: []string{"meta"},
	FlagSet: flag.NewFlagSet("schematics", flag.ExitOnError),
}

func init() {
	schematicsHelp = strings.TrimSpace(internal.Colorize(schematicsHelp))
	CmdSchematics.FlagSet.StringVar(&schematicsWasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")

	CmdSchematics.FlagSet.Usage = func() {
		fmt.Fprintln(CmdSchematics.FlagSet.Output(), schematicsHelp)
		CmdSchematics.FlagSet.PrintDefaults()
	}
	CmdRoot.AddCommand(CmdSchematics)
}

func SchematicsCommand(ctx context.Context, args []string) error {
	flagset := CmdSchematics.FlagSet

	flagset.Parse(args)

	if schematicsWasmPath == "" {
		flagset.Usage()
		return fmt.Errorf("--wasm is required")
	}

	if len(flagset.Args()) == 0 {
		flagset.Usage()
		return fmt.Errorf("no subcommand given to schematics")
	}

	subcmd, subargs := flagset.Arg(0), flagset.Args()[1:]

	// TODO: handle the schematics subcommand as a YokeCommand
	switch subcmd {
	case "ls":
		{
			schematics, err := yoke.ListSchematics(ctx, yoke.ListSchematicsParams{WasmURL: schematicsWasmPath})
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
				WasmURL: schematicsWasmPath,
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
				WasmPath: schematicsWasmPath,
				Name:     name,
				Input:    os.Stdin,
				CMD:      *cmd,
			})
		}
	default:
		return fmt.Errorf("unknown schematics subcommand: %q", subcmd)
	}
}
