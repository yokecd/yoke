package main

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {
	gvk := schema.FromAPIVersionAndKind("apiextensions.k8s.io/v1", "CustomResourceDefinition")
	fmt.Println(gvk.GroupKind().String())
}
