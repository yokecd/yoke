package main

import (
	"encoding/json"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// This implementation exists to test accessResourceMatchers.
// This release owns a single configmap but loads two secrets from different namespaces.
// The test will need to show that with appropriate matchers this works, and without matcher or with inappropriate matchers
// we get appropriate errors on admission.
func run() error {
	secretOne, err := k8s.Lookup[corev1.Secret](k8s.ResourceIdentifier{
		Name:       "one",
		Namespace:  "default",
		Kind:       "Secret",
		ApiVersion: "v1",
	})
	if err != nil {
		return fmt.Errorf("failed to lookup secret one: %w", err)
	}

	secretTwo, err := k8s.Lookup[corev1.Secret](k8s.ResourceIdentifier{
		Name:       "two",
		Namespace:  "custom",
		Kind:       "Secret",
		ApiVersion: "v1",
	})
	if err != nil {
		return fmt.Errorf("failed to lookup secret two: %w", err)
	}

	dataOne, err := json.Marshal(secretOne.Data)
	if err != nil {
		return err
	}

	dataTwo, err := json.Marshal(secretTwo.Data)
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(
		&corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "cm",
			},
			Data: map[string]string{
				"one": string(dataOne),
				"two": string(dataTwo),
			},
		},
	)
}
