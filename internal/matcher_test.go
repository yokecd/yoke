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

func TestUrlGlobMatching(t *testing.T) {
	cases := []struct {
		Name        string
		Globs       Globs
		Should      []string
		ShouldNot   []string
		ExpectedErr string
	}{
		{
			Name:   "empty input should match",
			Globs:  Globs{},
			Should: []string{"oci://ghcr.io/example/test", "banana"},
		},
		{
			Name:   "multiple concrete",
			Globs:  Globs{"oci://gar.io/example/test", "http://domain.com/vetted/ok"},
			Should: []string{"oci://gar.io/example/test", "http://domain.com/vetted/ok"},
			ShouldNot: []string{
				"http://ghcr.io/yokecd/yoke",
				"oci://domain.com/vetted/ok",
				"https://random",
				"oci://ghcr.io/yokecd/yokes",
			},
		},
		{
			Name:   "exact match on one item",
			Globs:  Globs{"oci://gar.io/example/test", "http://domain.com/vetted/ok"},
			Should: []string{"http://domain.com/vetted/ok"},
		},
		{
			Name:   "wildcard match",
			Globs:  Globs{"oci://ghcr.io/yokecd/*"},
			Should: []string{"oci://ghcr.io/yokecd/examples"},
		},
		{
			Name:      "scheme mismatch",
			Globs:     Globs{"oci://ghcr.io/yokecd/*"},
			Should:    []string{"oci://ghcr.io/yokecd/examples"},
			ShouldNot: []string{"https://ghcr.io/yokecd/examples"},
		},
		{
			Name:      "domain mismatch",
			Globs:     Globs{"https://example.com/*"},
			Should:    []string{"https://example.com/hello", "https://example.com/world"},
			ShouldNot: []string{"https://test.com/hello", "https://test.com/world"},
		},
		{
			Name:  "semantic major match",
			Globs: Globs{"oci://ghcr.io/repo/mod:2.*"},
			Should: []string{
				"oci://ghcr.io/repo/mod:2.1",
				"oci://ghcr.io/repo/mod:2.1.4",
			},
			ShouldNot: []string{
				"oci://ghcr.io/repo/mod",
				"oci://ghcr.io/repo/mod:1.0.0",
			},
		},
		{
			Name:      "ports",
			Globs:     Globs{"http://example.com:8080/*"},
			Should:    []string{"http://example.com:8080/foo", "http://example.com:8080/bar"},
			ShouldNot: []string{"http://example.com:3000/foo", "http://example.com:3000/bar"},
		},
		{
			Name:      "wildcard port",
			Globs:     Globs{"http://example.com:*/*"},
			Should:    []string{"http://example.com:8080/foo", "http://example.com:3000/bar"},
			ShouldNot: []string{"http://other.net:8080/foo", "http://other.net:3000/bar"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			for _, value := range tc.Should {
				require.Truef(t, tc.Globs.Match(value), "%q should have matched but did not", value)
			}
			for _, value := range tc.ShouldNot {
				require.Falsef(t, tc.Globs.Match(value), "%q should not have matched but did", value)
			}
		})
	}
}
