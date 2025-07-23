package main

import (
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var identifer k8s.ResourceIdentifier
	if err := json.NewDecoder(os.Stdin).Decode(&identifer); err != nil {
		return err
	}
	resource, err := k8s.Lookup[unstructured.Unstructured](identifer)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(resource)
}
