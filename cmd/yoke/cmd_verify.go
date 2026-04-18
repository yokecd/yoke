package main

import (
	_ "embed"
	"flag"
	"fmt"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_verify_help.txt
var verifyHelp string

var CmdVerify = &YokeCommand{
	Name:    "verify",
	FlagSet: flag.NewFlagSet("verify", flag.ExitOnError),
}

func init() {
	verifyHelp = strings.TrimSpace(internal.Colorize(verifyHelp))
	CmdRoot.AddCommand(CmdVerify)
}

func GetVerifyParams(args []string) (*yoke.VerifyParams, error) {
	flagset := flag.NewFlagSet("verify", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), verifyHelp)
		flagset.PrintDefaults()
	}

	var params yoke.VerifyParams
	flagset.StringVar(&params.KeyPath, "key", "", "Path to pulbic key pem used for verifying")

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
