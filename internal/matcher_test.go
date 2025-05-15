package internal

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMatchResource(t *testing.T) {
	resource := unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      "test",
				"namespace": "foo",
			},
		},
	}

	cases := []struct {
		Matcher  string
		Expected bool
	}{
		{
			Matcher:  "*",
			Expected: true,
		},
		{
			Matcher:  "",
			Expected: false,
		},
		{
			Matcher:  "foo/Deployment.apps:test",
			Expected: true,
		},
		{
			Matcher:  "Deployment.apps:test",
			Expected: true,
		},
		{
			Matcher:  "Deployment.apps",
			Expected: true,
		},
		{
			Matcher:  "Deployment",
			Expected: false,
		},
		{
			Matcher:  "bar/Deployment.apps",
			Expected: false,
		},
		{
			Matcher:  "Deployment.custom:test",
			Expected: false,
		},
		{
			Matcher:  "Deployment.apps:other",
			Expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Matcher, func(t *testing.T) {
			require.Equal(t, tc.Expected, MatchResource(&resource, tc.Matcher))
		})
	}
}
