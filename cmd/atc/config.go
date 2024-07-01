package main

import (
	"os"

	"github.com/davidmdm/conf"
)

type Config struct {
	KubeConfig string
}

func LoadConfig() (*Config, error) {
	var cfg Config

	parser := conf.MakeParser(conf.CommandLineArgs(), os.LookupEnv)

	conf.Var(parser, &cfg.KubeConfig, "KUBE")

	err := parser.Parse()
	return &cfg, err
}
