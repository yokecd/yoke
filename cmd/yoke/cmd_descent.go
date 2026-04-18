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

var CmdDescent = &YokeCommand{
	Name:    "descent",
	Aliases: []string{"down", "restore"},
	FlagSet: flag.NewFlagSet("descent", flag.ExitOnError),
}

var (
	release          string
	revisionID       int
	namespace        string
	wait             time.Duration
	poll             time.Duration
	lock             bool
	removeCRDs       bool
	removeNamespaces bool
	removeAll        bool
)

func init() {
	descentHelp = strings.TrimSpace(internal.Colorize(descentHelp))
	CmdDescent.FlagSet.StringVar(&namespace, "namespace", "", "release target namespace, defaults to context namespace if not provided")
	CmdDescent.FlagSet.DurationVar(&wait, "wait", 0, "time to wait for release to become ready")
	CmdDescent.FlagSet.DurationVar(&poll, "poll", 5*time.Second, "interval to poll resource state at. Used with --wait")
	CmdDescent.FlagSet.BoolVar(&lock, "lock", false, "if enabled does locks release before deploying revision (only prevents other locked runs from running).")
	CmdDescent.FlagSet.BoolVar(&removeAll, "remove-all", false, "enables pruning of crds and namespaces owned by the release if a new revision would orphan them.\nDestructive and dangerous use with caution.")
	CmdDescent.FlagSet.BoolVar(&removeCRDs, "remove-crds", false, "enables pruning of crds owned by the release.\nDestructive and dangerous use with caution.")
	CmdDescent.FlagSet.BoolVar(&removeNamespaces, "remove-namespaces", false, "enables pruning of namespaces owned by the release.\nDestructive and dangerous use with caution.")
	CmdDescent.FlagSet.Usage = func() {
		fmt.Fprintln(CmdDescent.FlagSet.Output(), descentHelp)
		CmdDescent.FlagSet.PrintDefaults()
	}

	CmdRoot.AddCommand(CmdDescent)
}

type DescentParams struct {
	GlobalSettings
	yoke.DescentParams
}

func GetDescentfParams(settings GlobalSettings, args []string) (*DescentParams, error) {
	flagset := CmdDescent.FlagSet

	params := DescentParams{
		GlobalSettings: settings,
	}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.Parse(args)
	params.Namespace = namespace
	params.Poll = poll
	params.Wait = wait
	params.Lock = lock
	params.RemoveCRDs = removeCRDs
	params.RemoveNamespaces = removeNamespaces

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
