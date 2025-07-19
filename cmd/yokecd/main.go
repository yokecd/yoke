package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/davidmdm/x/xcontext"

	"github.com/yokecd/yoke/cmd/yokecd/internal/plugin"
	"github.com/yokecd/yoke/cmd/yokecd/internal/svr"
	"github.com/yokecd/yoke/internal"
)

func main() {
	svrMode := flag.Bool("svr", false, "run module execute server")

	flag.Parse()

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	ctx = internal.WithDebugFlag(ctx, func(value bool) *bool { return &value }(true))

	run := func() error {
		if *svrMode {
			return svr.Run(ctx, svr.ConfigFromEnv())
		}
		return plugin.Run(ctx, plugin.ConfigFromEnv())
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if !errors.Is(err, xcontext.SignalCancelError{}) {
			os.Exit(1)
		}
	}
}
