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

type UnlatchParams struct {
	GlobalSettings
	yoke.UnlockParams
}

//go:embed cmd_unlatch_help.txt
var unlatchHelp string

var CmdUnlatch = &YokeCommand{
	Name:    "unlatch",
	Aliases: []string{"unlock"},
	FlagSet: flag.NewFlagSet("unlatch", flag.ExitOnError),
}

var unlatchParams UnlatchParams

func init() {
	maydayHelp = strings.TrimSpace(internal.Colorize(unlatchHelp))
	flagset := CmdUnlatch.FlagSet

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), maydayHelp)
		flagset.PrintDefaults()
	}
	flagset.StringVar(&unlatchParams.Namespace, "namespace", "default", "target namespace of release to remove")
	CmdRoot.AddCommand(CmdUnlatch)
}

func GetUnlatchParams(settings GlobalSettings, args []string) (*UnlatchParams, error) {
	flagset := CmdUnlatch.FlagSet

	unlatchParams.GlobalSettings = settings

	RegisterGlobalFlags(flagset, &unlatchParams.GlobalSettings)

	flagset.Parse(args)

	unlatchParams.Release = flagset.Arg(0)
	if unlatchParams.Release == "" {
		return nil, fmt.Errorf("release is required")
	}

	return &unlatchParams, nil
}

func Unlatch(ctx context.Context, params UnlatchParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return err
	}
	return commander.UnlockRelease(ctx, params.UnlockParams)
}
