package main

import (
	"encoding/json"
	"os"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
)

type Configs map[string]map[string]string

func main() {
	var input Configs
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}

	var resources flight.Resources
	for name, values := range input {
		resources = append(resources, &corev1.ConfigMap{
			TypeMeta:   v1.TypeMeta{Kind: "ConfigMap"},
			ObjectMeta: v1.ObjectMeta{Name: name},
			Data:       values,
		})
	}

	json.NewEncoder(os.Stdout).Encode(resources)
}
