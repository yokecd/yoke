package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_stow_help.txt
var stowHelp string

var CmdStow = NewCommand("stow", []string{"push"}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("stow", flag.ExitOnError)
	params := yoke.StowParams{}

	flagset.BoolVar(&params.Insecure, "insecure", false, "allows image references to be fetched without TLS")
	flagset.Func("tag", "comma separated list of tags", func(s string) error {
		params.Tags = append(params.Tags, strings.Split(s, ",")...)
		return nil
	})
	flagset.Usage = func() {
		stowHelp = strings.TrimSpace(internal.Colorize(stowHelp))
		fmt.Fprintln(flagset.Output(), stowHelp)
		flagset.PrintDefaults()
	}

	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {
		flagset.Parse(args)
		params.WasmFile = flagset.Arg(0)
		params.URL = flagset.Arg(1)

		if params.WasmFile == "" {
			return fmt.Errorf("wasm file must be specified as first argument")
		}
		if params.URL == "" {
			return fmt.Errorf("OCI url must be specified as second argument")
		}
		return yoke.Stow(ctx, params)
	}
})
