package main

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/davidmdm/conf"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"

	"dario.cat/mergo"
	"github.com/tidwall/sjson"
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

// structure of individual CMP parameters
type CmpParam struct {
	Name   string          `json:"name"`
	String string          `json:"string"`
	Array  []string        `json:"array"`
	Map    json.RawMessage `json:"map"`
}

var _ encoding.TextUnmarshaler = new(Parameters)

func (parameters *Parameters) UnmarshalText(data []byte) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("invalid config: %w", err)
		}
	}()

	var elems []CmpParam
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(&elems); err != nil {
		return err
	}

	build, _ := internal.Find(elems, func(param CmpParam) bool { return param.Name == "build" })

	if build.String != "" {
		parameters.Build, err = strconv.ParseBool(build.String)
		if err != nil {
			return fmt.Errorf("parsing parameter build: %w", err)
		}
	}

	wasm, _ := internal.Find(elems, func(param CmpParam) bool { return param.Name == "wasm" })
	parameters.Wasm = strings.TrimLeft(wasm.String, "/")

	if parameters.Wasm == "" && !parameters.Build {
		return fmt.Errorf("wasm parameter must be provided or build enabled")
	}

	if parameters.Wasm != "" && parameters.Build {
		return fmt.Errorf("wasm asset cannot be present and build enabled")
	}

	input, err := parseInput(elems)
	if err != nil {
		return err
	}
	parameters.Input = input

	args, _ := internal.Find(elems, func(param CmpParam) bool { return param.Name == "args" })
	parameters.Args = args.Array

	refs, _ := internal.Find(elems, func(param CmpParam) bool { return param.Name == "refs" })

	if refs.Map != nil {
		if err := json.Unmarshal(refs.Map, &parameters.Refs); err != nil {
			return err
		}
	}

	return nil
}

// parses `input` or `inputFiles` CMP parameters to compose the `Input` Flight param value
func parseInput(params []CmpParam) (string, error) {
	// value can be either `string` or `map`
	input, _ := internal.Find(params, func(p CmpParam) bool { return p.Name == "input" })

	if input.String != "" {
		// string `input` overrides other options
		return input.String, nil
	}

	var result map[string]any

	inputFiles, _ := internal.Find(params, func(p CmpParam) bool { return p.Name == "inputFiles" })

	for _, filePath := range inputFiles.Array {
		file, err := os.Open(filePath)
		if err != nil {
			return "", fmt.Errorf("could not read file '%v': %v", filePath, err)
		}
		defer file.Close()

		decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)

		var out map[string]any
		if err := decoder.Decode(&out); err != nil {
			return "", fmt.Errorf("could not parse YAML or JSON file '%v': %v", filePath, err)
		}

		if err := mergo.Merge(&result, out, mergo.WithOverride); err != nil {
			return "", fmt.Errorf("could not merge input files: %v", err)
		}
	}

	if len(input.Map) > 0 {
		var inputMap map[string]string

		if err := json.Unmarshal(input.Map, &inputMap); err != nil {
			return "", fmt.Errorf("could not parse map input: %v", err)
		}

		// `sjson` operates on a string JSON, not `map[string]any`, so serialize first or provide empty JSON object so it can set any values at all
		resultStr := "{}"
		if len(result) > 0 {
			bytes, err := json.Marshal(result)
			if err != nil {
				return "", fmt.Errorf("could not encode input into JSON: %v", err)
			}
			resultStr = string(bytes)
		}

		for key, value := range inputMap {
			var (
				val any
				err error
			)
			if err = yaml.Unmarshal([]byte(value), &val); err != nil {
				return "", fmt.Errorf("could not parse the input map entry %v=%v: %v", key, value, err)
			}
			resultStr, err = sjson.Set(resultStr, key, val)
			if err != nil {
				return "", fmt.Errorf("could not set input map entry %v=%v: %v", key, value, err)
			}
		}

		// already serialized into JSON, return directly
		return resultStr, nil
	}

	if len(result) > 0 {
		bytes, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("could not encode input into JSON: %v", err)
		}
		return string(bytes), nil
	}
	return "", nil
}

type Config struct {
	Application struct {
		Name      string
		Namespace string
	}
	Flight    Parameters
	Namespace string
	Env       map[string]string
}

func getConfig() (cfg Config, err error) {
	conf.Var(conf.Environ, &cfg.Namespace, "ARGOCD_NAMESPACE", conf.Default("default"))
	conf.Var(conf.Environ, &cfg.Application.Name, "ARGOCD_APP_NAME", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Application.Namespace, "ARGOCD_APP_NAMESPACE", conf.Required[string](true))
	conf.Var(conf.Environ, &cfg.Flight, "ARGOCD_APP_PARAMETERS", conf.Required[Parameters](true))
	err = conf.Environ.Parse()

	cfg.Env = map[string]string{}
	for _, e := range os.Environ() {
		envvar, ok := strings.CutPrefix(e, "ARGOCD_ENV_")
		if !ok {
			continue
		}
		k, v, ok := strings.Cut(envvar, "=")
		if !ok {
			continue
		}
		cfg.Env[k] = v
	}

	return
}
