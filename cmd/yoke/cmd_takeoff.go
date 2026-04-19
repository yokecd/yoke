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

type TakeoffParams struct {
	GlobalSettings
	yoke.TakeoffParams
}

//go:embed cmd_takeoff_help.txt
var takeoffHelp string

var CmdTakeoff = &YokeCommand{
	Name:    "takeoff",
	Aliases: []string{"up", "apply"},
	FlagSet: flag.NewFlagSet("takeoff", flag.ExitOnError),
}

var takeoffParams = TakeoffParams{}
var takeoffRemoveAll bool

func init() {
	takeoffHelp = strings.TrimSpace(internal.Colorize(takeoffHelp))
	takeoffParams.GlobalSettings = settings
	flagset := CmdTakeoff.FlagSet

	flagset.BoolVar(&takeoffParams.SendToStdout, "stdout", false, "execute the underlying wasm and outputs it to stdout but does not apply any resources to the cluster")
	flagset.BoolVar(&takeoffParams.DryRun, "dry", false, "only call the kubernetes api with dry-run; takes precedence over skip-dry-run.")
	flagset.BoolVar(&takeoffParams.SkipDryRun, "skip-dry-run", false, "disables running dry run to resources before applying them; ineffective if dry-run is true")
	flagset.BoolVar(&takeoffParams.ForceConflicts, "force-conflicts", false, "force apply changes on field manager conflicts")
	flagset.BoolVar(&takeoffParams.ForceOwnership, "force-ownership", false, "take ownership of resources during takeoff of resources even if they belong to another release")

	flagset.BoolVar(&takeoffParams.Lock, "lock", false, "if enabled does locks release before deploying revision (only prevents other locked runs from running).")
	flagset.BoolVar(&takeoffParams.CreateNamespace, "create-namespace", false, "create namespace of target release if not present")
	flagset.BoolVar(&takeoffParams.CrossNamespace, "cross-namespace", false, "allows releases to create resources in other namespaces than the target namespace")
	flagset.BoolVar(&takeoffParams.ClusterAccess.Enabled, "cluster-access", false, "allows flight access to the cluster during takeoff. Only applies when not directing output to stdout or to a local destination.")
	flagset.BoolVar(&takeoffParams.Flight.Insecure, "insecure", false, "allows image references to be fetched without TLS (only applies to oci urls)")
	flagset.Uint64Var(&takeoffParams.Flight.MaxMemoryMib, "max-memory-mib", 128, "max memory a flight is allowed to allocate at runtime. Max is 4096.")
	flagset.DurationVar(&takeoffParams.Flight.Timeout, "timeout", 10*time.Second, "timeout for flight execution. Setting to 0 keeps the default 10 seconds. To remove timeouts completely use a negative duration")

	flagset.BoolVar(&takeoffParams.DiffOnly, "diff-only", false, "show diff between current revision and would be applied state. Does not apply anything to cluster")
	flagset.BoolVar(&takeoffParams.Color, "color", term.IsTerminal(int(os.Stdout.Fd())), "use colored output in diffs")
	flagset.IntVar(&takeoffParams.Context, "context", 4, "number of lines of context in diff (ignored if not using --diff-only)")
	flagset.StringVar(&takeoffParams.Out, "out", "", "if present outputs flight resources to directory specified, if out is - outputs to standard out")
	flagset.StringVar(&takeoffParams.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")
	flagset.DurationVar(&takeoffParams.Wait, "wait", 0, "time to wait for release to be ready")
	flagset.DurationVar(&takeoffParams.Poll, "poll", 5*time.Second, "interval to poll resource state at. Used with --wait")

	flagset.IntVar(&takeoffParams.HistoryCapSize, "history-cap", 10, "max number of revisions to keep in release history. 0 or less is unbounded.")

	flagset.StringVar(&takeoffParams.Flight.CompilationCacheDir, "compilation-cache", "", "location to cache wasm compilations")
	flagset.StringVar(&takeoffParams.Checksum, "checksum", "", "sha256 checksum for desired module. If module does not match checksum takeoff will fail. Checksum can be inferred from oci tag or from  http basepath")
	flagset.StringVar(&takeoffParams.VerifyKeyPath, "verify", "", "path to public key or directory of keys to verify module signature against.")
	flagset.Func(
		"resource-access",
		"allows flights with cluster-access to read resources outside of the release that match pattern. This flag can be set many times and matchers can be comma separated.",
		func(s string) error {
			takeoffParams.ClusterAccess.ResourceMatchers = append(takeoffParams.ClusterAccess.ResourceMatchers, strings.Split(s, ",")...)
			return nil
		},
	)
	flagset.BoolVar(&takeoffRemoveAll, "remove-all", false, "enables pruning of crds and namespaces owned by the release if a new revision would orphan them.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&takeoffParams.RemoveCRDs, "remove-crds", false, "enables pruning of crds owned by the release.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&takeoffParams.RemoveNamespaces, "remove-namespaces", false, "enables pruning of namespaces owned by the release.\nDestructive and dangerous use with caution.")
	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), takeoffHelp)
		flagset.PrintDefaults()
	}
	CmdRoot.AddCommand(CmdTakeoff)
}

func GetTakeoffParams(settings GlobalSettings, source io.Reader, args []string) (*TakeoffParams, error) {
	flagset := CmdTakeoff.FlagSet
	takeoffParams.TakeoffParams.Flight = yoke.FlightParams{Input: source}
	RegisterGlobalFlags(flagset, &takeoffParams.GlobalSettings)

	args, takeoffParams.Flight.Args = internal.CutArgs(args)

	flagset.Parse(args)

	if takeoffRemoveAll {
		takeoffParams.RemoveCRDs = true
		takeoffParams.RemoveNamespaces = true
	}

	takeoffParams.Release = flagset.Arg(0)
	takeoffParams.Flight.Path = flagset.Arg(1)

	if takeoffParams.Release == "" {
		return nil, fmt.Errorf("release is required as first positional arg")
	}
	if takeoffParams.Flight.Input == nil && takeoffParams.Flight.Path == "" {
		return nil, fmt.Errorf("flight-path is required as second position arg")
	}

	return &takeoffParams, nil
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
