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

	TLS struct {
		CA         File
		ServerCert File
		ServerKey  File
	}
}

type File struct {
	Path string
	Data []byte
}

func LoadConfig() (*Config, error) {
	var cfg Config

	parser := conf.MakeParser(conf.CommandLineArgs(), os.LookupEnv)

	conf.Var(parser, &cfg.Port, "PORT", conf.Default(3000))
	conf.Var(parser, &cfg.KubeConfig, "KUBE")
	conf.Var(parser, &cfg.Concurrency, "CONCURRENCY", conf.Default(runtime.NumCPU()))
	conf.Var(parser, &cfg.CacheDir, "CACHE_DIR", conf.Default(os.TempDir()))

	conf.Var(parser, &cfg.TLS.CA.Path, "TLS_CA_CERT", conf.RequiredNonEmpty[string]())
	conf.Var(parser, &cfg.TLS.ServerCert.Path, "TLS_SERVER_CERT", conf.RequiredNonEmpty[string]())
	conf.Var(parser, &cfg.TLS.ServerKey.Path, "TLS_SERVER_KEY", conf.RequiredNonEmpty[string]())

	if err := parser.Parse(); err != nil {
		return nil, err
	}

	fs := conf.MakeParser(conf.FileSystem(conf.FileSystemOptions{}))

	conf.Var(fs, &cfg.TLS.CA.Data, cfg.TLS.CA.Path, conf.RequiredNonEmpty[[]byte]())
	conf.Var(fs, &cfg.TLS.ServerCert.Data, cfg.TLS.ServerCert.Path, conf.RequiredNonEmpty[[]byte]())
	conf.Var(fs, &cfg.TLS.ServerKey.Data, cfg.TLS.ServerKey.Path, conf.RequiredNonEmpty[[]byte]())

	if err := fs.Parse(); err != nil {
		return nil, err
	}

	cfg.Concurrency = max(cfg.Concurrency, 1)

	return &cfg, nil
}
