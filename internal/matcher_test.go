package internal

import (
	"strconv"
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

	cases := map[bool][]string{
		true: {
			"*",
			"foo/Deployment.apps:test",
			"Deployment.apps:test",
			"Deployment.apps",
			"*/*",
			"*/Deployment.apps",
			"foo/Deployment.apps:*",
			"*/Deployment.apps:*",
		},
		false: {
			"",
			"/",
			"Deployment",
			"bar/Deployment.apps",
			"Deployment.custom:test",
			"Deployment.apps:other",
			"bar/*",
		},
	}

	for ok, matchers := range cases {
		t.Run(strconv.FormatBool(ok), func(t *testing.T) {
			for _, matcher := range matchers {
				t.Run(matcher, func(t *testing.T) {
					require.Equal(t, ok, MatchResource(&resource, matcher))
				})
			}
		})
	}
}
