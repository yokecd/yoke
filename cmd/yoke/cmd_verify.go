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

var verifyParams yoke.VerifyParams

func init() {
	verifyHelp = strings.TrimSpace(internal.Colorize(verifyHelp))

	flagset := flag.NewFlagSet("verify", flag.ExitOnError)
	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), verifyHelp)
		flagset.PrintDefaults()
	}

	flagset.StringVar(&verifyParams.KeyPath, "key", "", "Path to pulbic key pem used for verifying")
	CmdRoot.AddCommand(CmdVerify)
}

func GetVerifyParams(args []string) (*yoke.VerifyParams, error) {
	flagset := flag.NewFlagSet("verify", flag.ExitOnError)

	flagset.Parse(args)

	verifyParams.WasmFile = flagset.Arg(0)

	if verifyParams.WasmFile == "" {
		return nil, fmt.Errorf("wasm file must be specified as first argument")
	}
	if verifyParams.KeyPath == "" {
		return nil, fmt.Errorf("key is required")
	}

	return &verifyParams, nil
}
