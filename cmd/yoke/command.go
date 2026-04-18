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
	SubCommands    []*YokeCommand
	CompletionFunc func([]string)
	Parent         *YokeCommand
}

// We might actually want to implement this as a map to make it blazingly fast
func (y *YokeCommand) AddCommand(sub *YokeCommand) {
	sub.Parent = y
	y.SubCommands = append(y.SubCommands, sub)
}

func (y YokeCommand) AllNames() []string {
	return append(y.Aliases, y.Name)
}
