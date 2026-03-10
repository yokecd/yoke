package main

import (
	_ "embed"
	"flag"
	"fmt"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_sign_help.txt
var signHelp string

func init() {
	signHelp = strings.TrimSpace(internal.Colorize(signHelp))
}

func GetSignParams(args []string) (*yoke.SignParams, error) {
	flagset := flag.NewFlagSet("sign", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), signHelp)
		flagset.PrintDefaults()
	}

	var params yoke.SignParams
	flagset.StringVar(&params.KeyPath, "key", "", "Path to private key pem used for signing")
	flagset.StringVar(&params.Out, "o", "", "output file to write signed wasm module. If omitted module will be signed in place.")

	flagset.Parse(args)

	params.WasmFile = flagset.Arg(0)

	if params.WasmFile == "" {
		return nil, fmt.Errorf("wasm file must be specified as first argument")
	}
	if params.KeyPath == "" {
		return nil, fmt.Errorf("key is required")
	}

	return &params, nil
}
