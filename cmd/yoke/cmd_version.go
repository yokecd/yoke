package main

import (
	"fmt"
	"slices"

	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/yokecd/yoke/internal"
)

func Version() error {
	tbl := table.NewWriter()
	tbl.SetStyle(table.StyleRounded)

	tbl.AppendRow(table.Row{"yoke", internal.Version()})
	tbl.AppendSeparator()

	modules := []string{
		"k8s.io/client-go",
		"github.com/tetratelabs/wazero",
	}

	slices.Sort(modules)

	for _, mod := range internal.Mods() {
		if slices.Contains(modules, mod.Path) {
			tbl.AppendRow(table.Row{mod.Path, mod.Version})
		}
	}

	fmt.Println(tbl.Render())

	return nil
}
