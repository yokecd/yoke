package main

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/yokecd/yoke/internal"
)

func Version(ctx context.Context) error {
	tbl := table.NewWriter()
	tbl.SetStyle(table.StyleRounded)

	tbl.AppendRow(table.Row{"yoke", internal.GetYokeVersion()})
	tbl.AppendRow(table.Row{"toolchain", internal.Info.GoVersion})

	modules := []string{
		"k8s.io/client-go",
		"github.com/tetratelabs/wazero",
	}

	for _, modPath := range modules {
		mod, ok := internal.Find(internal.Info.Deps, func(mod *debug.Module) bool { return mod.Path == modPath })
		if !ok {
			continue
		}
		tbl.AppendRow(table.Row{mod.Path, mod.Version})
	}

	_, err := fmt.Fprintln(internal.Stdout(ctx), tbl.Render())
	return err
}
