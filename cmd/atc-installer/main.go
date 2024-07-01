package main

import (
	"encoding/json"
	"fmt"
	"os"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	names := apiextensionsv1.CustomResourceDefinitionNames{
		Plural:   "flights",
		Singular: "flight",
		Kind:     "Flight",
	}

	group := "yoke.sh"

	crd := apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: "apiextensions.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Plural + "." + group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: names,
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: apiextensionsv1.JSONSchemaDefinitions{
								"spec": apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: apiextensionsv1.JSONSchemaDefinitions{
										"url": apiextensionsv1.JSONSchemaProps{
											Type: "string",
										},
										"args": apiextensionsv1.JSONSchemaProps{
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"},
											},
										},
										"input": apiextensionsv1.JSONSchemaProps{
											Type: "string",
										},
									},
									Required: []string{"url"},
								},
							},
							Required: []string{"spec"},
						},
					},
				},
			},
		},
	}

	return json.NewEncoder(os.Stdout).Encode(crd)
}
