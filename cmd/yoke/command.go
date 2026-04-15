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
	FlagsSet       *flag.FlagSet
	SubCommands    []*YokeCommand
	CompletionFunc func([]string)
}

// We might actually want to implement this as a map to make it blazingly fast
func (y *YokeCommand) AddCommand(sub *YokeCommand) {
	y.SubCommands = append(y.SubCommands, sub)
}

func (y YokeCommand) AllNames() []string {
	return append(y.Aliases, y.Name)
}
