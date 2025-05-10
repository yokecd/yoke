package internal

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseStaging(t *testing.T) {
	cases := []struct {
		Name   string
		Input  string
		Output Stages
		Err    string
	}{
		{
			Name:  "single yaml resource",
			Input: "kind: example",
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "example"}},
				},
			},
		},
		{
			Name:  "single json resource",
			Input: `{"kind": "example"}`,
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "example"}},
				},
			},
		},
		{
			Name:  "multi doc yaml resource",
			Input: "kind: example\n---\nkind: other",
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "example"}},
					&unstructured.Unstructured{Object: map[string]any{"kind": "other"}},
				},
			},
		},
		{
			Name:  "multi doc json resource",
			Input: "{\"kind\": \"example\"}\n---\n{\"kind\": \"other\"}",
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "example"}},
					&unstructured.Unstructured{Object: map[string]any{"kind": "other"}},
				},
			},
		},
		{
			Name:  "structured single stage",
			Input: "[{kind: one}, {kind: two}]",
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "one"}},
					&unstructured.Unstructured{Object: map[string]any{"kind": "two"}},
				},
			},
		},
		{
			Name:  "structured multi stage",
			Input: "[ [{kind: one}], [{kind: two}] ]",
			Output: Stages{
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "one"}},
				},
				{
					&unstructured.Unstructured{Object: map[string]any{"kind": "two"}},
				},
			},
		},
		{
			Name:  "invalid resource",
			Input: "hello",
			Err:   "input must be resource, list of resources, or list of list of resources",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			stages, err := ParseStages([]byte(tc.Input))
			if tc.Err != "" {
				require.EqualError(t, err, tc.Err)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, tc.Output, stages)
		})
	}
}
