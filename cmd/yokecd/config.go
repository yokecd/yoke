package main

import (
	"bytes"
	"encoding"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/davidmdm/conf"
	"github.com/yokecd/yoke/internal"
)

type Parameters struct {
	Build bool
	Wasm  string
	Input string
	Args  []string
}

var _ encoding.TextUnmarshaler = new(Parameters)

func (parameters *Parameters) UnmarshalText(data []byte) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("invalid config: %w", err)
		}
	}()

	type Param struct {
		Name   string   `json:"name"`
		String string   `json:"string"`
		Array  []string `json:"array"`
	}

	var elems []Param
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&elems); err != nil {
		return err
	}

	build, _ := internal.Find(elems, func(param Param) bool { return param.Name == "build" })

	if build.String != "" {
		parameters.Build, err = strconv.ParseBool(build.String)
		if err != nil {
			return fmt.Errorf("parsing parameter build: %w", err)
		}
	}

	wasm, _ := internal.Find(elems, func(param Param) bool { return param.Name == "wasm" })
	parameters.Wasm = strings.TrimLeft(wasm.String, "/")

	if parameters.Wasm == "" && !parameters.Build {
		return fmt.Errorf("wasm parameter must be provided or build enabled")
	}

	if parameters.Wasm != "" && parameters.Build {
		return fmt.Errorf("wasm asset cannot be present and build enabled")
	}

	input, _ := internal.Find(elems, func(param Param) bool { return param.Name == "input" })
	parameters.Input = input.String

	args, _ := internal.Find(elems, func(param Param) bool { return param.Name == "args" })
	parameters.Args = args.Array

	return nil
}

type Config struct {
	Application struct {
		Name      string
		Namespace string
	}
	Flight Parameters
}

func getConfig() (cfg Config, err error) {
	conf.Var(conf.Environ, &cfg.Application.Name, "ARGOCD_APP_NAME", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Application.Namespace, "ARGOCD_APP_NAMESPACE", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Flight, "ARGOCD_APP_PARAMETERS", conf.Required[Parameters](true))
	err = conf.Environ.Parse()
	return
}
