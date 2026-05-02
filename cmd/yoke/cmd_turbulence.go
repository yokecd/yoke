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

var CmdTurbulence = NewCommand("turbulence", []string{"drift", "diff"}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("turbulence", flag.ExitOnError)
	params := TurbulenceParams{}

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
	flagset.StringVar(&params.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")

	flagset.Usage = func() {
		turbulenceHelp = strings.TrimSpace(internal.Colorize(turbulenceHelp))
		fmt.Fprintln(flagset.Output(), turbulenceHelp)
		flagset.PrintDefaults()
	}
	return flagset, func(ctx context.Context, settings GlobalSettings, args []string) error {

		params.GlobalSettings = settings
		RegisterGlobalFlags(flagset, &params.GlobalSettings)

		flagset.Parse(args)

		params.Release = flagset.Arg(0)
		if params.Release == "" {
			return fmt.Errorf("release is required")
		}

		params.ConflictsOnly = params.ConflictsOnly || params.Fix
		return Turbulence(ctx, params)
	}
})

func Turbulence(ctx context.Context, params TurbulenceParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return err
	}
	return commander.Turbulence(ctx, params.TurbulenceParams)
}
