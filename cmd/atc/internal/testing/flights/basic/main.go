package main

import (
	"encoding/json"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var data map[string]string
	if err := json.NewDecoder(os.Stdin).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode input: %w", err)
	}

	return json.NewEncoder(os.Stdout).Encode(corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: flight.Release(),
		},
		Data: data,
	})
}
