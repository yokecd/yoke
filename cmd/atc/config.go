package main

import (
	"os"

	"github.com/davidmdm/conf"
)

type Config struct {
	KubeConfig  string
	Concurrency int
}

func LoadConfig() (*Config, error) {
	var cfg Config

	parser := conf.MakeParser(conf.CommandLineArgs(), os.LookupEnv)

	conf.Var(parser, &cfg.KubeConfig, "KUBE")
	conf.Var(parser, &cfg.Concurrency, "CONCURRENCY")

	if err := parser.Parse(); err != nil {
		return nil, err
	}

	cfg.Concurrency = max(cfg.Concurrency, 1)

	return &cfg, nil
}
