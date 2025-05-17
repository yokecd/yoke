// This lookup is unsafe as the flight can try and read arbitrary secrets and write them to a configmap.
// This example exists to test resource-access matchers. By default flights with cluster access can only read
// the resources that are owned by the release. Hence this flight with cluster-access can only read the configmap it outputs.
// It cannot read any sercrets from the cluster. However we can use the `-resource-access` flag to allow it access to sensitive data.
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

func run() error {
	identifier := k8s.ResourceIdentifier{
		Kind:       "Secret",
		ApiVersion: "v1",
	}
	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&identifier); err != nil {
		return fmt.Errorf("failed to decode input: %w", err)
	}

	secret, err := k8s.Lookup[corev1.Secret](identifier)
	if err != nil && !k8s.IsErrNotFound(err) {
		return err
	}

	data, err := json.Marshal(secret)
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: flight.Release(),
		},
		Data: map[string]string{
			"data": string(data),
		},
	})
}
