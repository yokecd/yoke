package main

import (
	"encoding/json"
	"os"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

func main() {
	json.NewEncoder(os.Stdout).Encode(flight.Resources{
		&corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "prune",
			},
		},
		&apiextensionsv1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{
				APIVersion: apiextensionsv1.SchemeGroupVersion.Identifier(),
				Kind:       "CustomResourceDefinition",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "prunes.test.com",
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "test.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "prunes",
					Singular: "prune",
					Kind:     "Prune",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: openapi.SchemaFor[struct{}](),
						},
					},
				},
			},
		},
	})
}
