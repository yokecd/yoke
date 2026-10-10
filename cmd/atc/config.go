package main

import (
	"os"
	"runtime"

	"github.com/davidmdm/conf"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/xcrypto"
)

type Config struct {
	KubeConfig  string
	Concurrency int
	Port        int

	ModuleAllowList        internal.Globs
	ModuleVerificationKeys xcrypto.PublicKeySet

	CacheFS string

	Service atc.ServiceDef

	DockerConfigSecretName string

	Verbose bool

	TLS TLSConfig

	DisableCustomReadiness bool
}

type File struct {
	Path string
	Data []byte
}

type TLSConfig struct {
	CA         File
	ServerCert File
	ServerKey  File
}

func LoadConfig() (*Config, error) {
	var cfg Config

	parser := conf.MakeParser(conf.CommandLineArgs(), os.LookupEnv)

	parser.Var(&cfg.Port, "PORT", conf.Default(3000))
	parser.Var(&cfg.KubeConfig, "KUBE")
	parser.Var(&cfg.Concurrency, "CONCURRENCY", conf.Default(runtime.GOMAXPROCS(0)))
	parser.Var(&cfg.Verbose, "VERBOSE")
	parser.Var(&cfg.CacheFS, "CACHE_FS", conf.Default(os.TempDir()))
	parser.Var(&cfg.ModuleAllowList, "MODULE_ALLOW_LIST")
	parser.Var(&cfg.DisableCustomReadiness, "DISABLE_CUSTOM_READINESS")
	parser.Var(&cfg.DockerConfigSecretName, "DOCKER_CONFIG_SECRET_NAME")

	parser.Var(&cfg.TLS.CA.Path, "TLS_CA_CERT", conf.RequiredNonEmpty[string]())
	parser.Var(&cfg.TLS.ServerCert.Path, "TLS_SERVER_CERT", conf.RequiredNonEmpty[string]())
	parser.Var(&cfg.TLS.ServerKey.Path, "TLS_SERVER_KEY", conf.RequiredNonEmpty[string]())
	parser.Var(&cfg.Service.Name, "SVC_NAME", conf.RequiredNonEmpty[string]())
	parser.Var(&cfg.Service.Namespace, "SVC_NAMESPACE", conf.RequiredNonEmpty[string]())
	parser.Var(&cfg.Service.Port, "SVC_PORT", conf.RequiredNonEmpty[int32]())

	var verificationKeyPath string
	parser.Var(&verificationKeyPath, "MODULE_VERIFICATION_KEYS_PATH")

	if err := parser.Parse(); err != nil {
		return nil, err
	}

	fs := conf.MakeParser(conf.FileSystem(conf.FileSystemOptions{}))

	fs.Var(&cfg.TLS.CA.Data, cfg.TLS.CA.Path, conf.RequiredNonEmpty[[]byte]())
	fs.Var(&cfg.TLS.ServerCert.Data, cfg.TLS.ServerCert.Path, conf.RequiredNonEmpty[[]byte]())
	fs.Var(&cfg.TLS.ServerKey.Data, cfg.TLS.ServerKey.Path, conf.RequiredNonEmpty[[]byte]())

	if verificationKeyPath != "" {
		fs.Var(&cfg.ModuleVerificationKeys, verificationKeyPath, conf.JSON[xcrypto.PublicKeySet], conf.Required[xcrypto.PublicKeySet](true))
	}

	if err := fs.Parse(); err != nil {
		return nil, err
	}

	cfg.Service.CABundle = cfg.TLS.CA.Data
	cfg.Concurrency = max(cfg.Concurrency, 1)

	return &cfg, nil
}
