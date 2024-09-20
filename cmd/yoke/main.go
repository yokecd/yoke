package main

import (
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
	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/home"
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
	settings := GlobalSettings{Debug: new(bool)}
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
	case "turbulence", "drift":
		{
			params, err := GetTurbulenceParams(settings, subcmdArgs)
			if err != nil {
				return err
			}
			return Turbulence(ctx, *params)
		}
	case "version":
		{
			return Version()
		}
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

type GlobalSettings struct {
	KubeConfigPath string
	Debug          *bool
}

func RegisterGlobalFlags(flagset *flag.FlagSet, settings *GlobalSettings) {
	flagset.StringVar(&settings.KubeConfigPath, "kubeconfig", home.Kubeconfig, "path to kube config")
	flagset.BoolVar(settings.Debug, "debug", false, "debug output mode")
}
