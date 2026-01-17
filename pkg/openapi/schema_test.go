package openapi_test

import (
	_ "embed"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"
)

func TestGenerateSchema(t *testing.T) {
	type Embedded struct {
		Embedded bool `json:"embed"`
	}

	type S struct {
		Embedded
		Name        string             `json:"name" MinLength:"3"`
		Age         int                `json:"age" Minimum:"18"`
		Labels      map[string]string  `json:"labels,omitempty"`
		Active      bool               `json:"active"`
		Choice      string             `json:"choice" Enum:"yes,no,toaster"`
		Rule        string             `json:"rule" XValidations:"[{\"rule\": \"has(self)\", \"message\":\"something\"}]"`
		Map         map[string]any     `json:"map"`
		IntOrString intstr.IntOrString `json:"intOrString,omitzero"`
		Quantity    resource.Quantity  `json:"quantity,omitzero"`
	}

	require.EqualValues(
		t,
		&apiext.JSONSchemaProps{
			Type: "object",
			Properties: apiext.JSONSchemaDefinitions{
				"name": {
					Type:      "string",
					MinLength: ptr.To[int64](3),
				},
				"age": {
					Type:    "integer",
					Minimum: ptr.To[float64](18),
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
				"embed": {
					Type: "boolean",
				},
				"map": {
					Type:                   "object",
					XPreserveUnknownFields: ptr.To(true),
				},
				"intOrString": {
					XIntOrString: true,
					AnyOf: []apiext.JSONSchemaProps{
						{Type: "string"},
						{Type: "integer"},
					},
				},
				"quantity": {
					XIntOrString: true,
					AnyOf: []apiext.JSONSchemaProps{
						{Type: "string"},
						{Type: "integer"},
					},
					Pattern: `^(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?$`,
				},
			},
			Required: []string{"name", "age", "active", "choice", "rule", "map"},
		},
		openapi.SchemaFrom(reflect.TypeFor[S]()),
	)
}

var golden bool

func init() {
	flag.BoolVar(&golden, "golden", false, "generate golden files")
}

//go:embed airway.golden.json
var airway string

func TestAirwaySchema(t *testing.T) {
	schema := openapi.SchemaFrom(reflect.TypeFor[v1alpha1.Airway]())

	data, err := json.MarshalIndent(schema, "", "  ")
	require.NoError(t, err)

	if golden {
		require.NoError(t, os.WriteFile("airway.golden.json", data, 0o644))
	}

	require.JSONEq(t, string(data), airway)
}

//go:embed flight.golden.json
var flight string

func TestFlightSchema(t *testing.T) {
	schema := openapi.SchemaFrom(reflect.TypeFor[v1alpha1.Flight]())

	data, err := json.MarshalIndent(schema, "", "  ")
	require.NoError(t, err)

	if golden {
		require.NoError(t, os.WriteFile("flight.golden.json", data, 0o644))
	}

	require.JSONEq(t, string(data), flight)
}
