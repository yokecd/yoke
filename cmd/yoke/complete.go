package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

func cleanArg(argIn string) string {
	return argIn
}

func printFlagCompletion(args []string, cmd *YokeCommand) {
	for _, flag := range getFlagCompletion(args, cmd) {
		fmt.Println(flag)
	}
}

// get the flags associated with a yokeCommand
// it takes the args slice after the YokeCommand
func getFlagCompletion(args []string, cmd *YokeCommand) []string {
	flagSetAll := make(map[string]bool)
	out := make([]string, 0)
	partial := strings.TrimLeft(args[len(args)-1], "-")
	if cmd.FlagSet == nil {
		return out
	}
	appendWithPrefix := func(f *flag.Flag, p string) {
		if p == "" || strings.HasPrefix(f.Name, p) {
			if !slices.Contains(args, "-"+f.Name) {
				flagSetAll["-"+f.Name] = true
			}
		}
	}
	// Iterate through all of the places we get flags from
	cur := cmd
	for cur != nil && cur.FlagSet != nil {
		cur.FlagSet.VisitAll(func(f *flag.Flag) {
			appendWithPrefix(f, partial)
		})
		cur = cur.Parent
	}

	for k := range flagSetAll {
		out = append(out, k)
	}
	return out
}

// given the args passed, yield all of the valid next top level cocmmands
func getCommandCompletions(args []string, cmd *YokeCommand) []*YokeCommand {
	out := make([]*YokeCommand, 0)
	outSet := make(map[string]*YokeCommand)
	partial := ""
	if len(args) > 0 {
		partial = args[len(args)-1]
	}
	if partial == "complete" || partial == "yoke" {
		partial = ""
	}
	for _, v := range cmd.SubCommands {
		if strings.HasPrefix(v.Name, partial) || partial == "" {
			outSet[v.Name] = v
		}
	}
	for _, cmd := range outSet {
		out = append(out, cmd)
	}
	return out
}

func printCommandCompletions(args []string, cmd *YokeCommand) {
	for _, cmd := range getCommandCompletions(args, cmd) {
		fmt.Println(cmd.Name)
	}
}

func Complete() {
	if len(os.Args) < 2 {
		return
	}
	if _, ok := os.LookupEnv("COMP_LINE"); !ok {
		fmt.Print(`
# Please add the following to your bashrc file to enable tab completions

function _yoke() {
  COMPREPLY=($(yoke complete $COMP_LINE));
};

complete -F _yoke yoke
`)
		return
	}

	argSet := make(map[string]bool)
	for _, arg := range os.Args {
		argSet[arg] = true
	}
	argsAfterComp := os.Args[2:]
	if len(argsAfterComp) > 1 && argsAfterComp[0] == "yoke" {
		argsAfterComp = argsAfterComp[1:]
	}
	cmd, rest := Seek(argsAfterComp)
	partial := ""
	if len(rest) > 0 {
		partial = rest[len(rest)-1]
	}
	if strings.HasPrefix(partial, "-") {
		printFlagCompletion(rest, cmd)
	}
	//if it's not a full command, get top-level completions
	printCommandCompletions(rest, cmd)
}
