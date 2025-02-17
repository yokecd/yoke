package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/cmd/atc-installer/installer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg := installer.Config{
		Version: "latest",
		Port:    3000,
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
			return err
		}
	}

	stages, err := installer.Run(cfg)
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(stages)
}
