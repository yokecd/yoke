package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// install.yaml downloaded from https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

//go:embed install.yaml
var install string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var resources []*unstructured.Unstructured
	for _, manifest := range strings.Split(install, "\n---\n") {
		var resource unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(manifest), &resource); err != nil {
			return err
		}
		resources = append(resources, &resource)
	}

	return json.NewEncoder(os.Stdout).Encode(resources)
}
