package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/davidmdm/x/xcontainer"
	"github.com/davidmdm/x/xcontext"
	"github.com/yokecd/yoke/cmd/images/engine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type Config struct {
	Tags     xcontainer.Set[string]
	Command  string
	Registry string
}

func ParseFlags() (*Config, error) {
	cfg := Config{
		Tags: xcontainer.ToSet([]string{"latest"}),
	}

	flag.StringVar(&cfg.Command, "image", "", "image to build: one of atc or yokecd")
	flag.StringVar(&cfg.Registry, "registry", "ghcr.io", "oci registry address")
	flag.Func("tag", "docker tags", func(text string) error {
		cfg.Tags.Add(strings.Split(text, ",")...)
		return nil
	})

	flag.Parse()

	if cfg.Command != "atc" && cfg.Command != "yokecd" {
		return nil, fmt.Errorf("-image must be one of yokecd or atc")
	}

	return &cfg, nil
}

func run() error {
	cfg, err := ParseFlags()
	if err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	engine, err := engine.NewEngine(ctx, cfg.Registry)
	if err != nil {
		return fmt.Errorf("failed to create builder engine: %w", err)
	}

	switch command := flag.Arg(0); command {
	case "", "build":
		return engine.Build(ctx, cfg.Command, cfg.Tags.Collect())

	case "publish":
		return engine.Publish(ctx, cfg.Command, cfg.Tags.Collect())
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}
