package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/home"
	"github.com/davidmdm/yoke/pkg/yoke"
)

type TakeoffFlightParams struct {
	Path      string
	Input     io.Reader
	Args      []string
	Namespace string
}

type TakeoffParams struct {
	GlobalSettings
	yoke.TakeoffParams
}

//go:embed cmd_takeoff_help.txt
var takeoffHelp string

func init() {
	takeoffHelp = strings.TrimSpace(internal.Colorize(takeoffHelp))
}

func GetTakeoffParams(settings GlobalSettings, source io.Reader, args []string) (*TakeoffParams, error) {
	flagset := flag.NewFlagSet("takeoff", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), takeoffHelp)
		flagset.PrintDefaults()
	}

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Flight: yoke.FlightParams{Input: source},
		},
	}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.BoolVar(&params.TestRun, "test-run", false, "test-run executes the underlying wasm and outputs it to stdout but does not apply any resources to the cluster")
	flagset.BoolVar(&params.SkipDryRun, "skip-dry-run", false, "disables running dry run to resources before applying them")
	flagset.BoolVar(&params.ForceConflicts, "force-conflicts", false, "force apply changes on field manager conflicts")
	flagset.BoolVar(&params.CreateCRDs, "create-crds", true, "applies custom resource definitions found in flights")
	flagset.BoolVar(&params.CreateNamespaces, "create-namespaces", false, "applies namespace resources found in flights")

	flagset.BoolVar(&params.DiffOnly, "diff-only", false, "show diff between current revision and would be applied state. Does not apply anything to cluster")
	flagset.BoolVar(&params.Color, "color", term.IsTerminal(int(os.Stdout.Fd())), "use colored output in diffs")
	flagset.IntVar(&params.Context, "context", 4, "number of lines of context in diff (ignored if not using --diff-only)")
	flagset.StringVar(&params.Out, "out", "", "if present outputs flight resources to directory specified, if out is - outputs to standard out")
	flagset.StringVar(&params.Flight.Namespace, "namespace", "default", "preferred namespace for resources if they do not define one")
	flagset.DurationVar(&params.Wait, "wait", 0, "time to wait for release to be ready")
	flagset.DurationVar(&params.Poll, "poll", 5*time.Second, "interval to poll resource state at. Used with --wait")

	flagset.StringVar(&params.Flight.CompilationCacheDir, "compilation-cache", "", "location to cache wasm compilations")

	args, params.Flight.Args = internal.CutArgs(args)

	flagset.Parse(args)

	params.Release = flagset.Arg(0)
	params.Flight.Path = flagset.Arg(1)

	if params.Release == "" {
		return nil, fmt.Errorf("release is required as first positional arg")
	}
	if params.Flight.Input == nil && params.Flight.Path == "" {
		return nil, fmt.Errorf("flight-path is required as second position arg")
	}

	return &params, nil
}

func TakeOff(ctx context.Context, params TakeoffParams) error {
	commander, err := yoke.FromKubeConfig(home.Kubeconfig)
	if err != nil {
		return err
	}
	return commander.Takeoff(ctx, params.TakeoffParams)
}
