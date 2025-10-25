package internal

import (
	"context"
	"io"
	"runtime/debug"
	"time"

	"github.com/davidmdm/ansi"
)

type debugKey struct{}

func WithDebugFlag(ctx context.Context, debug *bool) context.Context {
	return context.WithValue(ctx, debugKey{}, debug)
}

func Debug(ctx context.Context) ansi.Terminal {
	debug, _ := ctx.Value(debugKey{}).(*bool)
	if debug == nil || !*debug {
		return ansi.Terminal{Writer: io.Discard}
	}
	return ansi.Stderr
}

func DebugTimer(ctx context.Context, msg string) func() {
	start := time.Now()
	terminal := Debug(ctx)
	terminal.Printf("start: %s\n", msg)
	return func() { terminal.Printf("done:  %s: %s\n", msg, time.Since(start).Round(time.Millisecond)) }
}

var Info, _ = debug.ReadBuildInfo()

func GetYokeVersion() string {
	const path = "github.com/yokecd/yoke"
	if Info.Main.Path == path {
		return Info.Main.Version
	}
	for _, dep := range Info.Deps {
		if dep.Path == path {
			return dep.Version
		}
	}
	return "(unknown?)"
}
