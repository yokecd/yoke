package main

import (
	"os"
	"runtime"

	"github.com/davidmdm/conf"
)

type Config struct {
	KubeConfig  string
	Concurrency int
	Port        int
	CacheDir    string
}

func LoadConfig() (*Config, error) {
	var cfg Config

	parser := conf.MakeParser(conf.CommandLineArgs(), os.LookupEnv)

	conf.Var(parser, &cfg.Port, "PORT", conf.Default(3000))
	conf.Var(parser, &cfg.KubeConfig, "KUBE")
	conf.Var(parser, &cfg.Concurrency, "CONCURRENCY", conf.Default(runtime.NumCPU()))
	conf.Var(parser, &cfg.CacheDir, "CACHE_DIR", conf.Default(os.TempDir()))

	if err := parser.Parse(); err != nil {
		return nil, err
	}

	cfg.Concurrency = max(cfg.Concurrency, 1)

	return &cfg, nil
}
