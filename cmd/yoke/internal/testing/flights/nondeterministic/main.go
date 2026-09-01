package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
)

func main() {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}

	if err := json.NewEncoder(os.Stdout).Encode(corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: flight.Release(),
		},
		Data: map[string]string{
			"nonce":     hex.EncodeToString(nonce),
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		panic(err)
	}
}
