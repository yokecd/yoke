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

var CmdUnlatch = NewCommand("unlatch", []string{"unlock"}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("unlatch", flag.ExitOnError)
	params := UnlatchParams{}

	flagset.Usage = func() {
		maydayHelp = strings.TrimSpace(internal.Colorize(unlatchHelp))
		fmt.Fprintln(flagset.Output(), maydayHelp)
		flagset.PrintDefaults()
	}
	flagset.StringVar(&params.Namespace, "namespace", "default", "target namespace of release to remove")

	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {
		RegisterGlobalFlags(flagset, &params.GlobalSettings)
		flagset.Parse(args)
		params.Release = flagset.Arg(0)
		params.Release = flagset.Arg(0)
		if params.Release == "" {
			return fmt.Errorf("release is required")
		}
		commander, err := yoke.FromKubeConfigFlags(params.Kube)
		if err != nil {
			return err
		}
		return commander.UnlockRelease(ctx, params.UnlockParams)
	}
})
