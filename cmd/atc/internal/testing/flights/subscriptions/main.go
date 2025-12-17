package main

import (
	"encoding/json"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	subscribed := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "subscribed",
		},
	}

	if _, err := k8s.LookupResource(subscribed); err != nil && !k8s.IsErrNotFound(err) {
		return fmt.Errorf("failed to lookup resource we want to subscribe to: %w", err)
	}

	externalCM, err := k8s.Lookup[corev1.ConfigMap](k8s.ResourceIdentifier{
		ApiVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "external",
	})
	if err != nil && !k8s.IsErrNotFound(err) {
		return fmt.Errorf("failed to get external configmap: %w", err)
	}

	standard := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "standard",
		},
		Data: func() map[string]string {
			if externalCM == nil {
				return nil
			}
			return externalCM.Data
		}(),
	}

	return json.NewEncoder(os.Stdout).Encode(flight.Resources{subscribed, standard})
}
