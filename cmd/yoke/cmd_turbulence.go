package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/pkg/yoke"
)

type TurbulenceParams struct {
	GlobalSettings
	Release       string
	Context       int
	ConflictsOnly bool
	Fix           bool
	Color         bool
}

//go:embed cmd_turbulence_help.txt
var turbulenceHelp string

func init() {
	turbulenceHelp = strings.TrimSpace(internal.Colorize(turbulenceHelp))
}

func GetTurbulenceParams(settings GlobalSettings, args []string) (*TurbulenceParams, error) {
	flagset := flag.NewFlagSet("turbulence", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), turbulenceHelp)
		flagset.PrintDefaults()
	}

	params := TurbulenceParams{GlobalSettings: settings}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)
	flagset.IntVar(&params.Context, "context", 4, "number of lines of context in diff")
	flagset.BoolVar(
		&params.ConflictsOnly,
		"conflict-only",
		true,
		""+
			"only show turbulence for declared state.\n"+
			"If false, will show diff against state that was not declared;\n"+
			"such as server generated annotations, status, defaults and more",
	)
	flagset.BoolVar(&params.Fix, "fix", false, "fix the drift. If present conflict-only will be true.")
	flagset.BoolVar(&params.Color, "color", term.IsTerminal(int(os.Stdout.Fd())), "outputs diff with color")

	flagset.Parse(args)

	params.Release = flagset.Arg(0)
	if params.Release == "" {
		return nil, fmt.Errorf("release is required")
	}

	params.ConflictsOnly = params.ConflictsOnly || params.Fix

	return &params, nil
}

func Turbulence(ctx context.Context, params TurbulenceParams) error {
	commander, err := yoke.FromKubeConfig(params.KubeConfigPath)
	if err != nil {
		return err
	}
	return commander.Turbulence(ctx, yoke.TurbulenceParams{
		Release:       params.Release,
		Context:       params.Context,
		ConflictsOnly: params.ConflictsOnly,
		Fix:           params.Fix,
		Color:         params.Color,
	})
}
