package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

var validCommands = map[string]*YokeCommand{
	// fake alias to the root command
	"complete":   CmdRoot,
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

func FlagCompletion(args []string, cmd *YokeCommand) {
	partial := strings.TrimLeft(args[len(args)-1], "-")
	if cmd.FlagSet == nil {
		fmt.Println("DEBUG: flagset null")
		return
	}
	flag.VisitAll(func(f *flag.Flag) {
		if partial == "" || strings.HasPrefix(f.Name, partial) {
			// could optimize with hash map
			if !slices.Contains(args, f.Name) {
				fmt.Println("-" + f.Name)
			}
		}
	})
}

func commandsWithPrefix(argsMap map[string]bool, partial string) []string {
	out := make([]string, 0)
	for _, cmd := range CmdRoot.SubCommands {
		// FIXME we should just pass in args,
		_, ok := argsMap[cleanArg(cmd.Name)]
		if !ok {
			// TODO: also aliases
			if strings.HasPrefix(cmd.Name, partial) {
				out = append(out, cmd.Name)
			}
		}
	}
	return out
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
		partialClean := strings.TrimLeft(partial, "-")
		lastCmdString := ""
		if len(os.Args) > 2 {
			lastCmdString = os.Args[len(os.Args)-2]
		}
		fmt.Println("DEBUG: partial", partialClean)
		fmt.Println("DEBUG: lcs", lastCmdString)
		// partial was a full command, we should hop into a sub command completion
		lastCmd, ok := validCommands[lastCmdString]
		if ok {
			// looking for the next command now
			fmt.Printf("DEBUG: completing for %s %s\n", lastCmd.Name, partial)
			FlagCompletion(os.Args, lastCmd)
		}
	}
	//if it's not a full command, get top-level completions
	comps := commandsWithPrefix(currentArgs, partial)
	for _, c := range comps {
		fmt.Println(c)
	}
}
