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

//go:embed cmd_sign_help.txt
var signHelp string

var CmdSign = NewCommand("sign", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("sign", flag.ExitOnError)
	params := yoke.SignParams{}
	flagset.StringVar(&params.KeyPath, "key", "", "Path to private key pem used for signing")
	flagset.StringVar(&params.Out, "o", "", "output file to write signed wasm module. If omitted module will be signed in place")
	flagset.BoolVar(&params.Force, "f", false, "forcefully override existing signature on module")
	flagset.Usage = func() {
		signHelp = strings.TrimSpace(internal.Colorize(signHelp))
		fmt.Fprintln(flagset.Output(), signHelp)
		flagset.PrintDefaults()
	}
	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {

		flagset.Parse(args)

		params.WasmFile = flagset.Arg(0)

		if params.WasmFile == "" {
			return fmt.Errorf("wasm file must be specified as first argument")
		}
		if params.KeyPath == "" {
			return fmt.Errorf("key is required")
		}
		return yoke.Sign(params)
	}
})
