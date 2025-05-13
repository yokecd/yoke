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

func init() {
	stowHelp = strings.TrimSpace(internal.Colorize(stowHelp))
}

func GetStowParams(args []string) (*yoke.StowParams, error) {
	flagset := flag.NewFlagSet("stow", flag.ExitOnError)

	flagset.Usage = func() {
		_, _ = fmt.Fprintln(flagset.Output(), stowHelp)
		flagset.PrintDefaults()
	}

	var params yoke.StowParams

	flagset.BoolVar(&params.Insecure, "insecure", false, "allows image references to be fetched without TLS")
	flagset.Func("tag", "comma separated list of tags", func(s string) error {
		params.Tags = append(params.Tags, strings.Split(s, ",")...)
		return nil
	})
	if err := flagset.Parse(args); err != nil {
		return nil, err
	}

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
