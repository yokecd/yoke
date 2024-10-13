package main

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/davidmdm/conf"
	"github.com/yokecd/yoke/internal"
)

type Ref struct {
	Secret    string `json:"secret"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

type Parameters struct {
	Build bool
	Wasm  string
	Input string
	Refs  map[string]Ref
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
		Name   string          `json:"name"`
		String string          `json:"string"`
		Array  []string        `json:"array"`
		Map    json.RawMessage `json:"map"`
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

	refs, _ := internal.Find(elems, func(param Param) bool { return param.Name == "refs" })

	if refs.Map != nil {
		if err := json.Unmarshal(refs.Map, &parameters.Refs); err != nil {
			return err
		}
	}

	return nil
}

type Config struct {
	Application struct {
		Name      string
		Namespace string
	}
	Flight    Parameters
	Namespace string
}

func getConfig() (cfg Config, err error) {
	conf.Var(conf.Environ, &cfg.Namespace, "ARGOCD_NAMESPACE", conf.Default("default"))
	conf.Var(conf.Environ, &cfg.Application.Name, "ARGOCD_APP_NAME", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Application.Namespace, "ARGOCD_APP_NAMESPACE", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Flight, "ARGOCD_APP_PARAMETERS", conf.Required[Parameters](true))
	err = conf.Environ.Parse()
	return
}
