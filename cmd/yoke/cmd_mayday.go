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

type MaydayParams struct {
	GlobalSettings
	yoke.MaydayParams
}

//go:embed cmd_mayday_help.txt
var maydayHelp string

var CmdMayday = &YokeCommand{
	Name:    "mayday",
	Aliases: []string{"delete"},
	FlagSet: flag.NewFlagSet("mayday", flag.ExitOnError),
}
var (
	maydayNamespace        string
	maydayRemoveAll        bool
	maydayRemoveCRDs       bool
	maydayRemoveNamespaces bool
)

func init() {
	maydayHelp = strings.TrimSpace(internal.Colorize(maydayHelp))
	CmdMayday.FlagSet.StringVar(&maydayNamespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")
	CmdMayday.FlagSet.BoolVar(&maydayRemoveAll, "remove-all", false, "deletes crds and namespaces owned by the release. Destructive and dangerous use with caution.")
	CmdMayday.FlagSet.BoolVar(&maydayRemoveCRDs, "remove-crds", false, "deletes crds owned by the release. Destructive and dangerous use with caution.")
	CmdMayday.FlagSet.BoolVar(&maydayRemoveNamespaces, "remove-namespaces", false, "deletes namespaces owned by the release. Destructive and dangerous use with caution.")

	CmdMayday.FlagSet.Usage = func() {
		fmt.Fprintln(CmdMayday.FlagSet.Output(), maydayHelp)
		CmdMayday.FlagSet.PrintDefaults()
	}
	CmdRoot.AddCommand(CmdMayday)
}

func GetMaydayParams(settings GlobalSettings, args []string) (*MaydayParams, error) {
	flagset := CmdMayday.FlagSet

	params := MaydayParams{GlobalSettings: settings}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.Parse(args)

	if maydayRemoveAll {
		params.RemoveCRDs = true
		params.RemoveNamespaces = true
	}

	params.Release = flagset.Arg(0)
	if params.Release == "" {
		return nil, fmt.Errorf("release is required")
	}

	return &params, nil
}

func Mayday(ctx context.Context, params MaydayParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return fmt.Errorf("failed to instantiate k8 client: %w", err)
	}
	return commander.Mayday(ctx, params.MaydayParams)
}
