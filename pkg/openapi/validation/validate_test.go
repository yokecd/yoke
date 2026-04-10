package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/yokecd/yoke/pkg/openapi"
)

func TestValidate(t *testing.T) {
	testcases := []struct {
		Name   string
		Schema *apiext.JSONSchemaProps
		Value  any
		Err    string
	}{
		{
			Name: "valid value passes",
			Schema: openapi.SchemaFor[struct {
				Name string `json:"name"`
				Age  int    `json:"age"`
			}](),
			Value: map[string]any{"name": "alice", "age": 30},
		},
		{
			Name: "missing required field fails",
			Schema: openapi.SchemaFor[struct {
				Name string `json:"name"`
				Age  int    `json:"age"`
			}](),
			Value: map[string]any{"name": "alice"},
			Err:   "age: Required value",
		},
		{
			Name: "wrong type fails",
			Schema: openapi.SchemaFor[struct {
				Count int `json:"count"`
			}](),
			Value: map[string]any{"count": "not-a-number"},
			Err:   `count: Invalid value: "string": count in body must be of type integer`,
		},
		{
			Name: "enum violation fails",
			Schema: openapi.SchemaFor[struct {
				Mode string `json:"mode" Enum:"read,write"`
			}](),
			Value: map[string]any{"mode": "execute"},
			Err:   `mode: Unsupported value: "execute": supported values: "read", "write"`,
		},
		{
			Name: "enum valid value passes",
			Schema: openapi.SchemaFor[struct {
				Mode string `json:"mode" Enum:"read,write"`
			}](),
			Value: map[string]any{"mode": "read"},
		},
		{
			Name: "minimum violation fails",
			Schema: openapi.SchemaFor[struct {
				Age int `json:"age" Minimum:"18"`
			}](),
			Value: map[string]any{"age": 10},
			Err:   `age: Invalid value: 10: age in body should be greater than or equal to 18`,
		},
		{
			Name: "minimum valid value passes",
			Schema: openapi.SchemaFor[struct {
				Age int `json:"age" Minimum:"18"`
			}](),
			Value: map[string]any{"age": 18},
		},
		{
			Name: "maxLength violation fails",
			Schema: openapi.SchemaFor[struct {
				Name string `json:"name" MaxLength:"5"`
			}](),
			Value: map[string]any{"name": "toolongname"},
			Err:   "name: Too long: may not be more than 5 bytes",
		},
		{
			Name: "pattern violation fails",
			Schema: openapi.SchemaFor[struct {
				Code string `json:"code" Pattern:"^[A-Z]+$"`
			}](),
			Value: map[string]any{"code": "abc"},
			Err:   `code: Invalid value: "abc": code in body should match '^[A-Z]+$'`,
		},
		{
			Name: "pattern valid value passes",
			Schema: openapi.SchemaFor[struct {
				Code string `json:"code" Pattern:"^[A-Z]+$"`
			}](),
			Value: map[string]any{"code": "ABC"},
		},
		{
			Name: "optional field may be omitted",
			Schema: openapi.SchemaFor[struct {
				Name    string `json:"name"`
				Comment string `json:"comment,omitempty"`
			}](),
			Value: map[string]any{"name": "alice"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			err := Validate(tc.Schema, tc.Value)
			if tc.Err != "" {
				require.ErrorContains(t, err, tc.Err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateStrict(t *testing.T) {
	schema := apiext.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiext.JSONSchemaProps{
			"subobj": {
				Type: "object",
			},
			"subarr": {
				Type: "array",
				Items: &apiext.JSONSchemaPropsOrArray{
					Schema: &apiext.JSONSchemaProps{
						Type: "object",
					},
				},
			},
			"preserve": {
				Type:                   "object",
				XPreserveUnknownFields: new(true),
			},
		},
	}

	value := map[string]any{
		"anything": "goes",
		"subobj": map[string]any{
			"prop": 1,
		},
		"subarr": []map[string]any{
			{"prop": 2},
		},
		"preserve": map[string]any{
			"prop": 3,
		},
	}
	require.NoError(t, Validate(&schema, value))
	require.EqualError(
		t,
		ValidateStrict(&schema, value),
		strings.Join(
			[]string{
				"errors:",
				`  - <nil>: Invalid value: "anything": .anything in body is a forbidden property`,
				`  - subobj: Invalid value: "prop": subobj.prop in body is a forbidden property`,
				`  - subarr[0]: Invalid value: "prop": subarr[0].prop in body is a forbidden property`,
			},
			"\n",
		),
	)
}
