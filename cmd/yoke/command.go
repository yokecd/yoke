package main

import (
	"context"
	"flag"
)

type YokeCommandRunner interface {
	Run(ctx context.Context, settings GlobalSettings, subCommands string) error
}

// The YokeCommand struct represents a cli commmand
// It should have a name, alias, and subcommands
type YokeCommand struct {
	Name           string
	Aliases        []string
	FlagSet        *flag.FlagSet
	SubCommands    map[string]*YokeCommand
	CompletionFunc func([]string)
	Parent         *YokeCommand
	Runner         CmdRunner
}

// AddCommand registers sub into the parents SubCommands
func (y *YokeCommand) AddCommand(sub *YokeCommand) {
	sub.Parent = y
	y.SubCommands[sub.Name] = sub
	for _, alias := range sub.Aliases {
		_, alreadyThere := y.SubCommands[alias]
		if !alreadyThere {
			y.SubCommands[alias] = sub
		}
	}
}

// AllNames returns the name of a command and all of its aliases
func (y YokeCommand) AllNames() []string {
	return append(y.Aliases, y.Name)
}

type CmdRunner func(ctx context.Context, settings GlobalSettings, args []string) error
type CmdBuilder func(ctx context.Context) (*flag.FlagSet, CmdRunner)

func NewCommand(name string, aliases []string, builder CmdBuilder) *YokeCommand {
	flagset, runner := builder(context.Background())
	return &YokeCommand{
		Name:        name,
		Aliases:     aliases,
		FlagSet:     flagset,
		Runner:      runner,
		SubCommands: make(map[string]*YokeCommand),
	}
}

// Seek returns the YokeCommand and the commands after the found command
// for a given set of args
func Seek(args []string) (*YokeCommand, []string) {
	cmdPtr := CmdRoot
	var argsOut = args
	for i, arg := range args {
		nextCmd, ok := cmdPtr.SubCommands[arg]
		if ok {
			cmdPtr = nextCmd
			if i+1 < len(args) {
				argsOut = args[i+1:]
			} else {
				argsOut = []string{}
			}
		}
	}
	if cmdPtr == CmdRoot {
		if len(args) > 0 {
			argsOut = args[1:]
		}
	}
	return cmdPtr, argsOut
}
