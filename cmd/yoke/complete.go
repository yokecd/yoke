package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

var validCommands = map[string]*YokeCommand{
	"atc": CmdATC,
	"takeoff": {
		Name:    "takeoff",
		Aliases: []string{"up", "apply"},
	},
	"descent": {
		Name:    "descent",
		Aliases: []string{"down", "restore"},
	},
	"mayday": {
		Name:    "mayday",
		Aliases: []string{"delete"},
	},
	"blackbox": {
		Name:    "blackbox",
		Aliases: []string{"inspect"},
	},
	"turbulence": {
		Name:    "turbulence",
		Aliases: []string{"drift", "diff"},
	},
	"stow": {
		Name:    "stow",
		Aliases: []string{"push"},
	},
	"unlatch": {
		Name:    "unlatch",
		Aliases: []string{"unlock"},
	},
	"schematics": {
		Name:    "schematics",
		Aliases: []string{"meta"},
	},
	"sign": {
		Name: "sign",
	},
	"verify": {
		Name: "verify",
	},
	"version": {
		Name: "version",
	},
}

func cleanArg(argIn string) string {
	return argIn
}

func CmdCompletion() {
	root := CmdRoot
	for _, cmd := range root.SubCommands {
		fmt.Println(cmd.Name)
	}
}

func FlagCompletion(cmd YokeCommand, args []string) {
	// TODO: aliases
	i := slices.Index(args, cmd.Name)
	if i == -1 {
		return
	}
	rest := args[i:]
	partial := rest[len(rest)-1]
	cmd.FlagsSet.VisitAll(func(f *flag.Flag) {
		if partial == "" || strings.HasPrefix(f.Name, partial) {
			// could optimize with hash map
			if !slices.Contains(args, f.Name) {
				fmt.Println("-" + f.Name)
			}
		}
	})
}

func Complete() {
	if len(os.Args) < 2 {
	}
	currentArgs := make(map[string]bool)
	for _, arg := range os.Args {
		currentArgs[cleanArg(arg)] = true
	}
	partial := os.Args[len(os.Args)-1]
	fmt.Println("DEBUG: ", partial)
	fmt.Println("DEBUG: ", currentArgs)
	// partial was a full command, we should hop into a sub command completion
	lastCmd, ok := validCommands[partial]
	if ok {
		// looking for the next command now
		fmt.Printf("DEBUG: cleaned %s -> ''\n", partial)
		FlagCompletion(*lastCmd, os.Args)
	}
	// is it in a top level command
	for _, cmd := range CmdRoot.SubCommands {
		name := cmd.Name
		_, ok := currentArgs[cleanArg(name)]
		if !ok {
			// TODO: also aliases
			if strings.HasPrefix(name, partial) {
				fmt.Println(name)
			}
		}
	}
}
