package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/cmd/atc-installer/installer"
)

func main() {
	cfg := installer.Config{
		Version: "latest",
		Port:    3000,
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
			exit(err)
		}
	}

	if err := installer.Run(cfg); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
