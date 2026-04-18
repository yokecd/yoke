package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// TODO: construct from rootcmd's children
var validCommands = map[string]*YokeCommand{
	// fake alias to the root command
	"complete":   CmdRoot,
	"yoke":       CmdRoot,
	"atc":        CmdATC,
	"takeoff":    CmdTakeoff,
	"descent":    CmdDescent,
	"mayday":     CmdMayday,
	"blackbox":   CmdBlackbox,
	"turbulence": CmdTurbulence,
	"stow":       CmdStow,
	"unlatch":    CmdUnlatch,
	"schematics": CmdSchematics,
	"sign":       CmdSign,
	"verify":     CmdVerify,
	"version":    CmdVersion,
}

func cleanArg(argIn string) string {
	return argIn
}

func printFlagCompletion(args []string, cmd *YokeCommand) {
	for _, flag := range getFlagCompletion(args, cmd) {
		fmt.Println(flag)
	}
}

// get the flags associated with a yokeCommand
// it takes all of args slice and the YokeCommand
func getFlagCompletion(args []string, cmd *YokeCommand) []string {
	flagSetAll := make(map[string]bool)
	out := make([]string, 0)
	partial := strings.TrimLeft(args[len(args)-1], "-")
	if cmd.FlagSet == nil {
		return out
	}
	filterForPrefix := func(f *flag.Flag, p string) {
		if p == "" || strings.HasPrefix(f.Name, p) {
			if !slices.Contains(args, f.Name) {
				flagSetAll["-"+f.Name] = true
			}
		}
	}
	// Iterate through all of the places we get flags from
	// If we had more layers, we'd need to not just
	// FIXME: Actually traverse the tree upward
	// needs to be upward so that we don't print the wrong flags
	flag.VisitAll(func(f *flag.Flag) {
		filterForPrefix(f, partial)
	})
	// get flagset flag
	cmd.FlagSet.VisitAll(func(f *flag.Flag) {
		filterForPrefix(f, partial)
	})
	for k := range flagSetAll {
		out = append(out, k)
	}
	return out
}

// given the args passed, yield all of the valid next top level cocmmands
func getCommandCompletions(args []string) []*YokeCommand {
	out := make([]*YokeCommand, 0)
	partial := args[len(args)-1]
	if partial == "complete" || partial == "yoke" {
		partial = ""
	}
	// We've already completed the command
	cmd, ok := validCommands[partial]
	if ok {
		for _, c := range cmd.SubCommands {
			fmt.Println("\t sub:", c.Name)
			if strings.HasPrefix(c.Name, partial) || partial == "" {
				out = append(out, c)
			}
		}
		return out
	}
	for k, v := range validCommands {
		if strings.HasPrefix(k, partial) {
			out = append(out, v)
		}
	}
	return out
}

func printCommandCompletions(args []string) {
	for _, cmd := range getCommandCompletions(args) {
		fmt.Println(cmd.Name)
	}
}

func Complete() {
	if len(os.Args) < 2 {
	}
	currentArgs := make(map[string]bool)
	for _, arg := range os.Args {
		currentArgs[arg] = true
	}
	partial := os.Args[len(os.Args)-1]
	if strings.HasPrefix(partial, "-") {
		lastCmdString := ""
		if len(os.Args) > 2 {
			lastCmdString = os.Args[len(os.Args)-2]
		}
		// partial was a full command, we should hop into a sub command completion
		lastCmd, ok := validCommands[lastCmdString]
		if ok {
			// looking for the next command now
			printFlagCompletion(os.Args, lastCmd)
		}
	}
	//if it's not a full command, get top-level completions
	printCommandCompletions(os.Args)
}
