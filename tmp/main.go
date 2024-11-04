package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	if err != nil {
		panic(err)
	}

	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Kind: "Airway", Group: "yoke.cd"})
	if err != nil {
		panic(err)
	}

	intf := client.Dynamic.Resource(mapping.Resource)

	resource, err := intf.Get(context.Background(), "foos.examples.com", v1.GetOptions{})
	if err != nil {
		panic(err)
	}

	unstructured.SetNestedMap(resource.Object, map[string]any{"Ready": "True"}, "spec", "status")

	resource, err = intf.UpdateStatus(context.Background(), resource, v1.UpdateOptions{})
	if err != nil {
		panic(err)
	}

	json.NewEncoder(os.Stdout).Encode(resource)
}
