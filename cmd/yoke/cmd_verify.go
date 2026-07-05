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

//go:embed cmd_verify_help.txt
var verifyHelp string

var CmdVerify = NewCommand("verify", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("verify", flag.ExitOnError)
	params := yoke.VerifyParams{}
	flagset.Usage = func() {
		verifyHelp = strings.TrimSpace(internal.Colorize(verifyHelp))
		fmt.Fprintln(flagset.Output(), verifyHelp)
		flagset.PrintDefaults()
	}
	flagset.StringVar(&params.KeyPath, "key", "", "Path to pulbic key pem used for verifying")
	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {
		flagset.Parse(args)

		params.WasmFile = flagset.Arg(0)

		if params.WasmFile == "" {
			return fmt.Errorf("wasm file must be specified as first argument")
		}
		if params.KeyPath == "" {
			return fmt.Errorf("key is required")
		}

		return yoke.Verify(params)
	}
})
