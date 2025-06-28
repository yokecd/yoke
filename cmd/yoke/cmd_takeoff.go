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

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
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

	flagset.BoolVar(&params.SendToStdout, "stdout", false, "execute the underlying wasm and outputs it to stdout but does not apply any resources to the cluster")
	flagset.BoolVar(&params.DryRun, "dry", false, "only call the kubernetes api with dry-run; takes precedence over skip-dry-run.")
	flagset.BoolVar(&params.SkipDryRun, "skip-dry-run", false, "disables running dry run to resources before applying them; ineffective if dry-run is true")
	flagset.BoolVar(&params.ForceConflicts, "force-conflicts", false, "force apply changes on field manager conflicts")
	flagset.BoolVar(&params.ForceOwnership, "force-ownership", false, "take ownership of previously existing unowned resources.")

	flagset.BoolVar(&params.Lockless, "lockless", false, "if enabled does not lock release before deploying revision.")
	flagset.BoolVar(&params.CreateNamespace, "create-namespace", false, "create namespace of target release if not present")
	flagset.BoolVar(&params.CrossNamespace, "cross-namespace", false, "allows releases to create resources in other namespaces than the target namespace")
	flagset.BoolVar(&params.ClusterAccess, "cluster-access", false, "allows flight access to the cluster during takeoff. Only applies when not directing output to stdout or to a local destination.")
	flagset.BoolVar(&params.Flight.Insecure, "insecure", false, "allows image references to be fetched without TLS (only applies to oci urls)")

	flagset.BoolVar(&params.DiffOnly, "diff-only", false, "show diff between current revision and would be applied state. Does not apply anything to cluster")
	flagset.BoolVar(&params.Color, "color", term.IsTerminal(int(os.Stdout.Fd())), "use colored output in diffs")
	flagset.IntVar(&params.Context, "context", 4, "number of lines of context in diff (ignored if not using --diff-only)")
	flagset.StringVar(&params.Out, "out", "", "if present outputs flight resources to directory specified, if out is - outputs to standard out")
	flagset.StringVar(&params.Flight.Namespace, "namespace", "default", "preferred namespace for resources if they do not define one")
	flagset.DurationVar(&params.Wait, "wait", 0, "time to wait for release to be ready")
	flagset.DurationVar(&params.Poll, "poll", 5*time.Second, "interval to poll resource state at. Used with --wait")

	flagset.IntVar(&params.HistoryCapSize, "history-cap", 10, "max number of revisions to keep in release history. 0 or less is unbounded.")

	flagset.StringVar(&params.Flight.CompilationCacheDir, "compilation-cache", "", "location to cache wasm compilations")

	flagset.Func(
		"resource-access",
		"allows flights with cluster-access to read resources outside of the release that match pattern. This flag can be set many times and matchers can be comma separated.",
		func(s string) error {
			params.ClusterResourceAccess = append(params.ClusterResourceAccess, strings.Split(s, ",")...)
			return nil
		},
	)

	var removeAll bool
	flagset.BoolVar(&removeAll, "remove-all", false, "enables pruning of crds and namespaces owned by the release if a new revision would orphan them.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveCRDs, "remove-crds", false, "enables pruning of crds owned by the release.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveNamespaces, "remove-namespaces", false, "enables pruning of namespaces owned by the release.\nDestructive and dangerous use with caution.")

	args, params.Flight.Args = internal.CutArgs(args)

	flagset.Parse(args)

	if removeAll {
		params.RemoveCRDs = true
		params.RemoveNamespaces = true
	}

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
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return err
	}

	// We want the CLI to stream stderr back to the user instead of buffering.
	params.Flight.Stderr = internal.Stderr(ctx)

	return commander.Takeoff(ctx, params.TakeoffParams)
}
