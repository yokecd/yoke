package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"

	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	secretName := flight.Release() + "-example"

	secret, err := k8s.Lookup[corev1.Secret](k8s.ResourceIdentifier{
		Name:       secretName,
		Namespace:  flight.Namespace(),
		Kind:       "Secret",
		ApiVersion: "v1",
	})
	if err != nil && !k8s.IsErrNotFound(err) {
		return fmt.Errorf("failed to lookup secret: %v", err)
	}

	if secret != nil {
		return json.NewEncoder(os.Stdout).Encode(secret)
	}

	return json.NewEncoder(os.Stdout).Encode(corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		StringData: map[string]string{
			"example": RandomString(),
		},
	})
}

func RandomString() string {
	buf := make([]byte, 6)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}
