package main

import (
	_ "embed"
	"flag"
	"fmt"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_stow_help.txt
var stowHelp string

var CmdStow = &YokeCommand{
	Name:     "stow",
	Aliases:  []string{"push"},
	FlagsSet: flag.NewFlagSet("stow", flag.ExitOnError),
}

func init() {
	stowHelp = strings.TrimSpace(internal.Colorize(stowHelp))
	CmdRoot.AddCommand(CmdStow)
}

func GetStowParams(args []string) (*yoke.StowParams, error) {
	flagset := CmdStow.FlagsSet

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), stowHelp)
		flagset.PrintDefaults()
	}

	var params yoke.StowParams

	flagset.BoolVar(&params.Insecure, "insecure", false, "allows image references to be fetched without TLS")
	flagset.Func("tag", "comma separated list of tags", func(s string) error {
		params.Tags = append(params.Tags, strings.Split(s, ",")...)
		return nil
	})
	flagset.Parse(args)

	params.WasmFile = flagset.Arg(0)
	params.URL = flagset.Arg(1)

	if params.WasmFile == "" {
		return nil, fmt.Errorf("wasm file must be specified as first argument")
	}
	if params.URL == "" {
		return nil, fmt.Errorf("OCI url must be specified as second argument")
	}

	return &params, nil
}
