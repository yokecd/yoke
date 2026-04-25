package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

func init() {
	CmdSchematics.AddCommand(CmdSchematicsGet)
	CmdSchematics.AddCommand(CmdSchematicsLs)
	CmdSchematics.AddCommand(CmdSchematicsSet)
}

//go:embed cmd_schematics_help.txt
var schematicsHelp string

var CmdSchematics = NewCommand("schematics", []string{"meta"}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("schematics", flag.ExitOnError)
	var wasmPath string
	flagset.StringVar(&wasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")
	flagset.Usage = func() {
		schematicsHelp = strings.TrimSpace(internal.Colorize(schematicsHelp))
		fmt.Fprintln(flagset.Output(), schematicsHelp)
		flagset.PrintDefaults()
	}

	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {
		flagset.Parse(args)
		return fmt.Errorf("subcommand is required")
	}
})

var CmdSchematicsLs = NewCommand("ls", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("schematics ls", flag.ExitOnError)
	var wasmPath string
	flagset.StringVar(&wasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")
	flagset.Usage = func() {
		flagset.Usage()
		flagset.PrintDefaults()
	}
	return flagset, func(ctx context.Context, settings GlobalSettings, _ []string) error {
		// FIXME:
		// This is so incredibily janky, but I can't think of another way to do this
		args := os.Args
		idxBase := slices.Index(args, "schematics")
		if idxBase >= 0 {
			args = os.Args[idxBase+1:]
		} else {
			return fmt.Errorf("orphaned subcommand")
		}

		flagset.Parse(args)
		schematics, err := yoke.ListSchematics(ctx, yoke.ListSchematicsParams{WasmURL: wasmPath})
		if err != nil {
			return fmt.Errorf("failed to list schematics: %w", err)
		}
		for _, schematic := range schematics {
			fmt.Fprintln(internal.Stdout(ctx), schematic)
		}
		return nil
	}
})

var CmdSchematicsGet = NewCommand("get", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("get", flag.ExitOnError)

	var wasmPath string
	flagset.StringVar(&wasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")
	flagset.Usage = func() {
		flagset.Usage()
		flagset.PrintDefaults()
	}

	return flagset, func(ctx context.Context, settings GlobalSettings, _ []string) error {
		//FIXME
		args := os.Args
		idxBase := slices.Index(args, "schematics")
		if idxBase >= 0 {
			args = os.Args[idxBase+1:]
		} else {
			return fmt.Errorf("orphaned subcommand")
		}
		flagset.Parse(args)
		idxGet := slices.Index(flagset.Args(), "get")
		name := ""
		if idxGet >= 0 {
			name = flagset.Arg(idxGet + 1)
		}
		if name == "" {
			return fmt.Errorf("name of schematics property is required")
		}
		data, err := yoke.GetSchematic(ctx, yoke.GetSchematicParams{
			WasmURL: wasmPath,
			Name:    name,
		})
		if err != nil {
			return fmt.Errorf("failed to get schematics: %w", err)
		}
		_, err = internal.Stdout(ctx).Write(data)
		return err
	}
})

var CmdSchematicsSet = NewCommand("set", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("set", flag.ExitOnError)
	cmd := flagset.Bool("cmd", false, "marks the input as command args to be executed to generate the schematic data")

	var wasmPath string
	flagset.StringVar(&wasmPath, "wasm", "", "path to wasm file. http(s), and oci urls are supported")
	flagset.Usage = func() {
		flagset.Usage()
		flagset.PrintDefaults()
	}

	return flagset, func(ctx context.Context, settings GlobalSettings, _ []string) error {
		//FIXME
		args := os.Args
		idxBase := slices.Index(args, "schematics")
		if idxBase >= 0 {
			args = os.Args[idxBase+1:]
		} else {
			return fmt.Errorf("orphaned subcommand")
		}

		_ = flagset.Parse(args)
		idxGet := slices.Index(flagset.Args(), "set")
		name := ""
		if idxGet >= 0 {
			name = flagset.Arg(idxGet + 1)
		}
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
})
