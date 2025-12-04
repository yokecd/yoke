package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

//go:embed cmd_descent_help.txt
var descentHelp string

func init() {
	descentHelp = strings.TrimSpace(internal.Colorize(descentHelp))
}

type DescentParams struct {
	GlobalSettings
	yoke.DescentParams
}

func GetDescentfParams(settings GlobalSettings, args []string) (*DescentParams, error) {
	flagset := flag.NewFlagSet("descent", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), descentHelp)
		flagset.PrintDefaults()
	}

	params := DescentParams{
		GlobalSettings: settings,
	}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.StringVar(&params.Namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")
	flagset.DurationVar(&params.Wait, "wait", 0, "time to wait for release to become ready")
	flagset.DurationVar(&params.Poll, "poll", 5*time.Second, "interval to poll resource state at. Used with --wait")
	flagset.BoolVar(&params.Lock, "lock", false, "if enabled does locks release before deploying revision (only prevents other locked runs from running).")

	var removeAll bool
	flagset.BoolVar(&removeAll, "remove-all", false, "enables pruning of crds and namespaces owned by the release if a new revision would orphan them.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveCRDs, "remove-crds", false, "enables pruning of crds owned by the release.\nDestructive and dangerous use with caution.")
	flagset.BoolVar(&params.RemoveNamespaces, "remove-namespaces", false, "enables pruning of namespaces owned by the release.\nDestructive and dangerous use with caution.")

	flagset.Parse(args)

	if removeAll {
		params.RemoveCRDs = true
		params.RemoveNamespaces = true
	}

	params.Release = flagset.Arg(0)
	if params.Release == "" {
		return nil, fmt.Errorf("release is required as first positional arg")
	}

	if len(flagset.Args()) < 2 {
		return nil, fmt.Errorf("revision is required as second positional arg")
	}

	revisionID, err := strconv.Atoi(flagset.Arg(1))
	if err != nil {
		return nil, fmt.Errorf("revision must be an integer ID: %w", err)
	}

	params.RevisionID = revisionID

	return &params, nil
}

func Descent(ctx context.Context, params DescentParams) error {
	commander, err := yoke.FromKubeConfigFlags(params.Kube)
	if err != nil {
		return fmt.Errorf("failed to instantiate k8 client: %w", err)
	}
	return commander.Descent(ctx, params.DescentParams)
}
