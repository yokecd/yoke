package main

import (
	"cmp"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/davidmdm/x/xcontext"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		if internal.IsWarning(err) {
			return
		}
		os.Exit(1)
	}
}

//go:embed cmd_help.txt
var rootHelp string

var CmdRoot = NewCommand("yoke", []string{}, func(ctx context.Context) (*flag.FlagSet, CmdRunner) {
	flagset := flag.NewFlagSet("yoke", flag.ExitOnError)
	flagset.Usage = func() {
		rootHelp = strings.TrimSpace(internal.Colorize(rootHelp))
		fmt.Fprintln(flag.CommandLine.Output(), rootHelp)
		flagset.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	runner := func(ctx context.Context, settings GlobalSettings, args []string) error {
		RegisterGlobalFlags(flagset, &settings)
		flagset.Parse(args)
		if len(flagset.Args()) > 0 {
			return fmt.Errorf("unknown command: %s", flagset.Arg(0))
		}
		if len(flagset.Args()) == 0 {
			flagset.Usage()
		}
		return fmt.Errorf("no command provided")
	}
	return flagset, runner
})

func init() {
	CmdRoot.AddCommand(CmdATC)
	CmdRoot.AddCommand(CmdBlackbox)
	CmdRoot.AddCommand(CmdDescent)
	CmdRoot.AddCommand(CmdMayday)
	CmdRoot.AddCommand(CmdSchematics)
	CmdRoot.AddCommand(CmdSign)
	CmdRoot.AddCommand(CmdStow)
	CmdRoot.AddCommand(CmdTakeoff)
	CmdRoot.AddCommand(CmdTurbulence)
	CmdRoot.AddCommand(CmdVersion)
	CmdRoot.AddCommand(CmdUnlatch)
	CmdRoot.AddCommand(CmdVerify)
}

func run() error {
	settings := GlobalSettings{
		Debug: new(bool),
		Kube:  genericclioptions.NewConfigFlags(false),
	}

	if len(os.Args) > 1 && os.Args[1] == "complete" {
		Complete()
		return nil
	}

	CmdRoot.FlagSet.Parse(os.Args)

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT)
	defer cancel()

	ctx = internal.WithDebugFlag(ctx, settings.Debug)

	cmd, subCmdArgs := Seek(CmdRoot.FlagSet.Args())

	if cmd == nil || cmd.Runner == nil {
		return fmt.Errorf("unknown command")
	}

	return cmd.Runner(ctx, settings, subCmdArgs)
}

type GlobalSettings struct {
	Kube  *genericclioptions.ConfigFlags
	Debug *bool
}

func RegisterGlobalFlags(flagset *flag.FlagSet, settings *GlobalSettings) {
	flagset.StringVar(settings.Kube.KubeConfig, "kubeconfig", cmp.Or(*settings.Kube.KubeConfig, os.Getenv("KUBECONFIG"), home.Kubeconfig), "path to kube config")
	flagset.StringVar(settings.Kube.Context, "kube-context", *settings.Kube.Context, "kubernetes context to use")
	flagset.BoolVar(settings.Debug, "debug", *settings.Debug, "debug output mode")
}
