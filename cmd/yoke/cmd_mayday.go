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

func init() {
	maydayHelp = strings.TrimSpace(internal.Colorize(maydayHelp))
}

func GetMaydayParams(settings GlobalSettings, args []string) (*MaydayParams, error) {
	flagset := flag.NewFlagSet("mayday", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), maydayHelp)
		flagset.PrintDefaults()
	}

	params := MaydayParams{GlobalSettings: settings}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.StringVar(&params.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")

	var removeAll bool
	flagset.BoolVar(&removeAll, "remove-all", false, "deletes crds and namespaces owned by the release. Destructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveCRDs, "remove-crds", false, "deletes crds owned by the release. Destructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveNamespaces, "remove-namespaces", false, "deletes namespaces owned by the release. Destructive and dangerous use with caution.")

	flagset.Parse(args)

	if removeAll {
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
