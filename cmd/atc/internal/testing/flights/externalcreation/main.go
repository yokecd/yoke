package main

import (
	"encoding/json"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type Spec struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type Status struct {
	Msg string `json:"msg"`
}

type CopyJob struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata"`
	Spec              Spec   `json:"spec"`
	Status            Status `json:"status"`
}

// This program aims to test that external creation events work.
// This program will wait for a specific configmap to exist and copy its data into a new one.
func run() error {
	var job CopyJob
	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&job); err != nil {
		return fmt.Errorf("failed to decode stdin into cr: %w", err)
	}

	result := flight.Resources{&job}

	source, err := k8s.Lookup[corev1.ConfigMap](k8s.ResourceIdentifier{
		Name:       job.Spec.Source,
		Namespace:  "default",
		Kind:       "ConfigMap",
		ApiVersion: "v1",
	})
	if err != nil {
		if !k8s.IsErrNotFound(err) {
			return fmt.Errorf("failed to lookup source configmap: %w", err)
		}
		job.Status.Msg = "source does not exist: waiting for it to be created."
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	result = append(result, &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Spec.Target,
			Namespace: "default",
		},
		Data: source.Data,
	})

	job.Status.Msg = "copy complete"

	return json.NewEncoder(os.Stdout).Encode(result)
}
