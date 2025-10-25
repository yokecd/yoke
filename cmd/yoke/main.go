package main

import (
	"cmp"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/davidmdm/x/xcontext"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/pkg/yoke"
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

func init() {
	rootHelp = strings.TrimSpace(internal.Colorize(rootHelp))
}

func run() error {
	settings := GlobalSettings{
		Debug: new(bool),
		Kube:  genericclioptions.NewConfigFlags(false),
	}

	RegisterGlobalFlags(flag.CommandLine, &settings)

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), rootHelp)
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	flag.Parse()

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT)
	defer cancel()

	ctx = internal.WithDebugFlag(ctx, settings.Debug)

	if len(flag.Args()) == 0 {
		flag.Usage()
		return fmt.Errorf("no command provided")
	}

	subcmdArgs := flag.Args()[1:]

	switch cmd := flag.Arg(0); cmd {
	case "atc":
		return ATC(ctx, GetAtcParams(settings, subcmdArgs))
	case "takeoff", "up", "apply":
		{
			var source io.Reader
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				source = os.Stdin
			}
			params, err := GetTakeoffParams(settings, source, subcmdArgs)
			if err != nil {
				return err
			}
			return TakeOff(ctx, *params)
		}
	case "descent", "down", "restore":
		{
			params, err := GetDescentfParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Descent(ctx, *params)
		}
	case "mayday", "delete":
		{
			params, err := GetMaydayParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Mayday(ctx, *params)
		}
	case "blackbox", "inspect":
		{
			params, err := GetBlackBoxParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Blackbox(ctx, *params)
		}
	case "turbulence", "drift", "diff":
		{
			params, err := GetTurbulenceParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Turbulence(ctx, *params)
		}
	case "stow", "push":
		{
			params, err := GetStowParams(subcmdArgs)
			if err != nil {
				return err
			}
			return yoke.Stow(ctx, *params)
		}
	case "unlatch", "unlock":
		{
			params, err := GetUnlatchParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Unlatch(ctx, *params)
		}
	case "schematics", "meta":
		{
			return SchematicsCommand(ctx, subcmdArgs)
		}

	case "version":
		{
			return Version(ctx)
		}
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

type GlobalSettings struct {
	Kube  *genericclioptions.ConfigFlags
	Debug *bool
}

func RegisterGlobalFlags(flagset *flag.FlagSet, settings *GlobalSettings) {
	flagset.StringVar(settings.Kube.KubeConfig, "kubeconfig", cmp.Or(os.Getenv("KUBECONFIG"), home.Kubeconfig), "path to kube config")
	flagset.StringVar(settings.Kube.Context, "kube-context", "", "kubernetes context to use")
	flagset.BoolVar(settings.Debug, "debug", false, "debug output mode")
}
