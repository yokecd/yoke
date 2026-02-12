package internal

import (
	"strconv"
	"strings"
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
	makeGlobs := func(values ...string) (globs URLGlobs) {
		require.NoError(t, globs.UnmarshalText([]byte(strings.Join(values, ","))))
		return
	}

	cases := []struct {
		Name        string
		Globs       URLGlobs
		Should      []string
		ShouldNot   []string
		ExpectedErr string
	}{
		{
			Name:   "empty input should match",
			Globs:  URLGlobs{},
			Should: []string{"oci://ghcr.io/example/test", "banana"},
		},
		{
			Name:   "multiple concrete",
			Globs:  makeGlobs("oci://gar.io/example/test", "http://domain.com/vetted/ok"),
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
			Globs:  makeGlobs("oci://gar.io/example/test", "http://domain.com/vetted/ok"),
			Should: []string{"http://domain.com/vetted/ok"},
		},
		{
			Name:   "wildcard match",
			Globs:  makeGlobs("oci://ghcr.io/yokecd/*"),
			Should: []string{"oci://ghcr.io/yokecd/examples"},
		},
		{
			Name:      "scheme mismatch",
			Globs:     makeGlobs("oci://ghcr.io/yokecd/*"),
			Should:    []string{"oci://ghcr.io/yokecd/examples"},
			ShouldNot: []string{"https://ghcr.io/yokecd/examples"},
		},
		{
			Name:      "domain mismatch",
			Globs:     makeGlobs("https://example.com/*"),
			Should:    []string{"https://example.com/hello", "https://example.com/world"},
			ShouldNot: []string{"https://test.com/hello", "https://test.com/world"},
		},
		{
			Name:  "semantic major match",
			Globs: makeGlobs("oci://ghcr.io/repo/mod:2.*"),
			Should: []string{
				"oci://ghcr.io/repo/mod:2.1",
				"oci://ghcr.io/repo/mod:2.1.4",
			},
			ShouldNot: []string{
				"oci://ghcr.io/repo/mod",
				"oci://ghcr.io/repo/mod:1.0.0",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			for _, value := range tc.Should {
				ok, err := tc.Globs.Match(value)
				if tc.ExpectedErr != "" {
					require.Contains(t, err, tc.ExpectedErr)
					return
				}
				require.NoError(t, err)
				require.Truef(t, ok, "%q should have matched but did not", value)
			}
			for _, value := range tc.ShouldNot {
				ok, err := tc.Globs.Match(value)
				if tc.ExpectedErr != "" {
					require.Contains(t, err, tc.ExpectedErr)
					return
				}
				require.NoError(t, err)
				require.Falsef(t, ok, "%q should not have matched but did", value)
			}
		})
	}
}
