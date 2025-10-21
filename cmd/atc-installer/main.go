package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"

	"golang.org/x/mod/semver"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	jyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/cmd/atc-installer/installer"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	skipVersionCheck := flag.Bool("skip-version-check", false, "skips checking for minimum version required")
	schema := flag.Bool("schema", false, "show input schema")
	flag.Parse()

	if *schema {
		return encodeAsYaml(os.Stdout, openapi.SchemaFrom(reflect.TypeFor[installer.Config]()))
	}

	if !*skipVersionCheck {
		if version := flight.YokeVersion(); semver.Compare(version, "v0.14.0") < 0 {
			return fmt.Errorf("minimum version required to run this flight is v0.14.0 but got %s", version)
		}
	}

	cfg := installer.Config{
		Version: "latest",
		Port:    3000,
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := jyaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
			return err
		}
	}

	resources, err := installer.Run(cfg)
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(resources)
}

func encodeAsYaml(dst io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	return yaml.NewEncoder(dst).Encode(obj)
}
