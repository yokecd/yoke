package openapi

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestGenerateSchema(t *testing.T) {
	type S struct {
		Name   string            `json:"name" MinLength:"3"`
		Age    int               `json:"age" Minimum:"18"`
		Labels map[string]string `json:"labels,omitempty"`
		Active bool              `json:"active"`
		Choice string            `json:"choice" Enum:"yes,no,toaster"`
		Rule   string            `json:"rule" XValidations:"[{\"rule\": \"has(self)\", \"message\":\"something\"}]"`
	}

	require.EqualValues(
		t,
		&apiext.JSONSchemaProps{
			Type: "object",
			Properties: apiext.JSONSchemaDefinitions{
				"name": {
					Type:      "string",
					MinLength: ptr[int64](3),
				},
				"age": {
					Type:    "integer",
					Minimum: ptr[float64](18),
				},
				"active": {
					Type: "boolean",
				},
				"labels": {
					Type: "object",
					AdditionalProperties: &apiext.JSONSchemaPropsOrBool{
						Schema: &apiext.JSONSchemaProps{Type: "string"},
					},
				},
				"choice": {
					Type: "string",
					Enum: []apiext.JSON{
						{Raw: []byte(`"yes"`)},
						{Raw: []byte(`"no"`)},
						{Raw: []byte(`"toaster"`)},
					},
				},
				"rule": {
					Type: "string",
					XValidations: apiext.ValidationRules{
						{
							Rule:    "has(self)",
							Message: "something",
						},
					},
				},
			},
			Required: []string{"name", "age", "active", "choice", "rule"},
		},
		SchemaFrom(reflect.TypeOf(S{})),
	)
}
