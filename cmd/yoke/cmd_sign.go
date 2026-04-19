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

var CmdSign = &YokeCommand{
	Name:    "sign",
	FlagSet: flag.NewFlagSet("sign", flag.ExitOnError),
}

var (
	signKeyPath string
	signOut     string
	signForce   bool
)

func init() {
	signHelp = strings.TrimSpace(internal.Colorize(signHelp))

	CmdSign.FlagSet.StringVar(&signKeyPath, "key", "", "Path to private key pem used for signing")
	CmdSign.FlagSet.StringVar(&signOut, "o", "", "output file to write signed wasm module. If omitted module will be signed in place")
	CmdSign.FlagSet.BoolVar(&signForce, "f", false, "forcefully override existing signature on module")
	CmdSign.FlagSet.Usage = func() {
		fmt.Fprintln(CmdSign.FlagSet.Output(), signHelp)
		CmdSign.FlagSet.PrintDefaults()
	}
	CmdRoot.AddCommand(CmdSign)
}

func GetSignParams(args []string) (*yoke.SignParams, error) {
	flagset := CmdSign.FlagSet

	params := yoke.SignParams{
		KeyPath: signKeyPath,
		Out:     signOut,
		Force:   signForce,
	}

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
