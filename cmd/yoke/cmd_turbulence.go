package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

type TurbulenceParams struct {
	GlobalSettings
	yoke.TurbulenceParams
}

//go:embed cmd_turbulence_help.txt
var turbulenceHelp string

var CmdTurbulence = &YokeCommand{
	Name:    "turbulence",
	Aliases: []string{"drift", "diff"},
	FlagSet: flag.NewFlagSet("turbulence", flag.ExitOnError),
}
var turbulencParams TurbulenceParams

func init() {
	turbulenceHelp = strings.TrimSpace(internal.Colorize(turbulenceHelp))
	flagset := CmdTurbulence.FlagSet
	flagset.IntVar(&turbulencParams.Context, "context", 4, "number of lines of context in diff")
	flagset.BoolVar(
		&turbulencParams.ConflictsOnly,
		"conflict-only",
		true,
		""+
			"only show turbulence for declared state.\n"+
			"If false, will show diff against state that was not declared;\n"+
			"such as server generated annotations, status, defaults and more",
	)
	flagset.BoolVar(&turbulencParams.Fix, "fix", false, "fix the drift. If present conflict-only will be true.")
	flagset.BoolVar(&turbulencParams.Color, "color", term.IsTerminal(int(os.Stdout.Fd())), "outputs diff with color")
	flagset.StringVar(&turbulencParams.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), turbulenceHelp)
		flagset.PrintDefaults()
	}
	CmdRoot.AddCommand(CmdTurbulence)
}

func GetTurbulenceParams(settings GlobalSettings, args []string) (*TurbulenceParams, error) {
	flagset := CmdTurbulence.FlagSet

	turbulencParams.GlobalSettings = settings
	RegisterGlobalFlags(flagset, &turbulencParams.GlobalSettings)

	flagset.Parse(args)

	turbulencParams.Release = flagset.Arg(0)
	if turbulencParams.Release == "" {
		return nil, fmt.Errorf("release is required")
	}

	turbulencParams.ConflictsOnly = turbulencParams.ConflictsOnly || turbulencParams.Fix

	return &turbulencParams, nil
}

func Turbulence(ctx context.Context, params TurbulenceParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return err
	}
	return commander.Turbulence(ctx, params.TurbulenceParams)
}
