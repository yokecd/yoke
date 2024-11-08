package openapi

import (
	"reflect"
	"testing"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/stretchr/testify/require"
)

func TestGenerateSchema(t *testing.T) {
	type S struct {
		Name   string            `json:"name"`
		Age    int               `json:"age"`
		Labels map[string]string `json:"labels,omitempty"`
		Active bool              `json:"active"`
	}

	require.EqualValues(
		t,
		&apiext.JSONSchemaProps{
			Type: "object",
			Properties: apiext.JSONSchemaDefinitions{
				"name": apiext.JSONSchemaProps{
					Type: "string",
				},
				"age": apiext.JSONSchemaProps{
					Type: "integer",
				},
				"active": apiext.JSONSchemaProps{
					Type: "boolean",
				},
				"labels": apiext.JSONSchemaProps{
					Type: "object",
					AdditionalProperties: &apiext.JSONSchemaPropsOrBool{
						Schema: &apiext.JSONSchemaProps{Type: "string"},
					},
				},
			},
			Required: []string{"name", "age", "active"},
		},
		SchemaFrom(reflect.TypeOf(S{})),
	)
}
