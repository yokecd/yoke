package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRef(t *testing.T) {
	type Expected struct {
		Name      string
		Namespace string
		GK        string
	}
	cases := []struct {
		Name   string
		Ref    string
		Output Expected
	}{
		{
			Name: "full",
			Ref:  "ns/kind.group:name",
			Output: Expected{
				Name:      "name",
				Namespace: "ns",
				GK:        "kind.group",
			},
		},
		{
			Name: "gk only",
			Ref:  "kind.group",
			Output: Expected{
				GK:        "kind.group",
				Name:      "",
				Namespace: "",
			},
		},
		{
			Name: "namespace only",
			Ref:  "default/",
			Output: Expected{
				Name:      "",
				Namespace: "default",
				GK:        "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ns, gk, name := ParseRef(tc.Ref)
			require.Equal(t, tc.Output.Name, name)
			require.Equal(t, tc.Output.Namespace, ns)
			require.Equal(t, tc.Output.GK, gk)
		})
	}
}
