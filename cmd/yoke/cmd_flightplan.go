package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_flightplan_help.txt
var flightplanHelp string

func init() {
	flightplanHelp = strings.TrimSpace(internal.Colorize(flightplanHelp))
}

func GetFlightPlanParams(settings GlobalSettings, source io.Reader, args []string) (*TakeoffParams, error) {
	flagset := flag.NewFlagSet("takeoff", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), flightplanHelp)
		flagset.PrintDefaults()
	}

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Flight: yoke.FlightParams{Input: source},
		},
	}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.StringVar(&params.Out, "out", "", "if present outputs flight resources to directory at path specified. If unspecified writes single multi-doc yaml document to stdout")
	flagset.StringVar(&params.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")

	flagset.BoolVar(&params.SendToStdout, "raw", false, "execute the underlying wasm and outputs the raw data to stdout exactly as the module produces it.")
	flagset.BoolVar(&params.Flight.Deterministic, "deterministic", false, "run the flight with deterministic WASI entropy and clock sources")
	flagset.BoolVar(&params.Flight.Insecure, "insecure", false, "allows image references to be fetched without TLS (only applies to oci urls)")
	flagset.Uint64Var(&params.Flight.MaxMemoryMib, "max-memory-mib", 128, "max memory a flight is allowed to allocate at runtime. Max is 4096.")
	flagset.DurationVar(&params.Flight.Timeout, "timeout", 10*time.Second, "timeout for flight execution. Setting to 0 keeps the default 10 seconds. To remove timeouts completely use a negative duration")
	flagset.StringVar(&params.Flight.CompilationCacheDir, "compilation-cache", "", "location to cache wasm compilations")

	flagset.StringVar(&params.Checksum, "checksum", "", "sha256 checksum for desired module. If module does not match checksum takeoff will fail. Checksum can be inferred from oci tag or from  http basepath")
	flagset.StringVar(&params.VerifyKeyPath, "verify", "", "path to public key or directory of keys to verify module signature against.")

	flagset.BoolVar(&params.ClusterAccess.Enabled, "cluster-access", false, "allows flight access to the cluster during takeoff. Only applies when not directing output to stdout or to a local destination.")
	flagset.Func(
		"resource-access",
		"allows flights with cluster-access to read resources outside of the release that match pattern. This flag can be set many times and matchers can be comma separated.",
		func(s string) error {
			params.ClusterAccess.ResourceMatchers = append(params.ClusterAccess.ResourceMatchers, strings.Split(s, ",")...)
			return nil
		},
	)

	args, params.Flight.Args = internal.CutArgs(args)

	flagset.Parse(args)

	if params.Out == "" {
		params.Out = "-"
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

func Flightplan(ctx context.Context, params TakeoffParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		fmt.Fprintf(internal.Stderr(ctx), "failed to instantiate a kubernetes client: %v\n", err)
		fmt.Fprintln(internal.Stderr(ctx), "proceeding with flightplan, but certain features will be unavailable such as cluster access")
		fmt.Fprintln(internal.Stderr(ctx))

		commander = &yoke.Commander{}
	}

	// We want the CLI to stream stderr back to the user instead of buffering.
	params.Flight.Stderr = internal.Stderr(ctx)

	return commander.Takeoff(ctx, params.TakeoffParams)
}
