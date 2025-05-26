package flight

import (
	_ "embed"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yokecd/yoke/pkg/openapi"
	sigyaml "sigs.k8s.io/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStatusMarshalling(t *testing.T) {
	cases := []struct {
		Name     string
		Status   Status
		Expected string
	}{
		{
			Name: "conditions only",
			Status: Status{
				Conditions: []metav1.Condition{
					{
						Type:   "Ready",
						Status: metav1.ConditionTrue,
					},
				},
			},
			Expected: `{
				"conditions": [
					{
						"type": "Ready",
						"status": "True",
						"lastTransitionTime": null,
						"reason": "",
						"message": ""
					}
				]
			}`,
		},
		{
			Name: "with props",
			Status: Status{
				Conditions: []metav1.Condition{
					{
						Type:   "Ready",
						Status: metav1.ConditionTrue,
					},
				},
				Props: map[string]any{
					"user":       "defined",
					"conditions": "should not appear",
				},
			},
			Expected: `{
				"user": "defined",
				"conditions": [
					{
						"type": "Ready",
						"status": "True",
						"lastTransitionTime": null,
						"reason": "",
						"message": ""
					}
				]
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			data, _ := json.Marshal(tc.Status)
			require.JSONEq(t, tc.Expected, string(data))

			delete(tc.Status.Props, "conditions")

			if len(tc.Status.Props) == 0 {
				tc.Status.Props = nil
			}

			var status Status
			json.Unmarshal(data, &status)
			require.Equal(t, tc.Status, status)
		})
	}
}

//go:embed status_schema.yaml
var expectedSchema string

var golden bool

func init() {
	flag.BoolVar(&golden, "golden", false, "generate golden file")
}

func TestStatusSchema(t *testing.T) {
	schema := openapi.SchemaFrom(reflect.TypeFor[Status]())

	data, err := sigyaml.Marshal(schema)
	require.NoError(t, err)

	if golden {
		require.NoError(t, os.WriteFile("status_schema.yaml", data, 0644))
	}

	require.Equal(t, expectedSchema, string(data))
}
