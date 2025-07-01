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

func init() {
	maydayHelp = strings.TrimSpace(internal.Colorize(unlatchHelp))
}

func GetUnlatchParams(settings GlobalSettings, args []string) (*UnlatchParams, error) {
	flagset := flag.NewFlagSet("unlatch", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), maydayHelp)
		flagset.PrintDefaults()
	}

	params := UnlatchParams{GlobalSettings: settings}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.StringVar(&params.Namespace, "namespace", "default", "target namespace of release to remove")

	flagset.Parse(args)

	params.Release = flagset.Arg(0)
	if params.Release == "" {
		return nil, fmt.Errorf("release is required")
	}

	return &params, nil
}

func Unlatch(ctx context.Context, params UnlatchParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return err
	}
	return commander.UnlockRelease(ctx, params.UnlockParams)
}
