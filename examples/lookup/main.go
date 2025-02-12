package main

import (
	"crypto/rand"
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
	secretName := flight.Release() + "-example"

	secret, err := k8s.Lookup[corev1.Secret](k8s.ResourceIdentifier{
		ApiVersion: "v1",
		Kind:       "Secret",
		Name:       secretName,
		Namespace:  flight.Namespace(),
	})
	if err != nil && !k8s.IsErrNotFound(err) {
		return fmt.Errorf("failed to lookup secret: %v", err)
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
			"password": func() string {
				if secret != nil {
					// if the secret already exists we want to reuse the example value instead of generating a new random string.
					return string(secret.Data["password"])
				}
				// Since the secret does not exist we need to generate a new password via the power of entropy!
				return RandomString()
			}(),
		},
	})
}

func RandomString() string {
	buf := make([]byte, 6)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}
