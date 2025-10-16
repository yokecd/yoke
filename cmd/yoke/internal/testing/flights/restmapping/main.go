package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var input struct {
		APIVersion string
		Kind       string
	}

	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&input); err != nil {
		return fmt.Errorf("failed to decode input: %w", err)
	}

	mapping, err := k8s.GetRestMapping(input.APIVersion, input.Kind)
	if err != nil {
		return fmt.Errorf("failed to get rest mapping: %w", err)
	}

	return json.NewEncoder(os.Stdout).Encode(corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Data: map[string]string{
			"resource":   mapping.Resource,
			"namespaced": strconv.FormatBool(mapping.Namespaced),
		},
	})
}
