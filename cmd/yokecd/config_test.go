package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigUnmarshalling(t *testing.T) {
	cases := []struct {
		Name     string
		Input    string
		Expected Parameters
		Error    string
	}{
		{
			Name:  "empty array",
			Input: `[]`,
			Error: "invalid config: wasm parameter must be provided or build enabled",
		},
		{
			Name: "build and wasm together",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: build, string: 'true' },
			]`,
			Error: "invalid config: wasm asset cannot be present and build enabled",
		},
		{
			Name: "build is non boolean string",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: build, string: 'hello world' },
			]`,
			Error: `invalid config: parsing parameter build: strconv.ParseBool: parsing "hello world": invalid syntax`,
		},
		{
			Name: "invalid args",
			Input: `[
				{ name: wasm, string: value },
				{ name: args, array: hello },
			]`,
			Error: "invalid config: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go struct field Param.array of type []string",
		},
		{
			Name: "full wasm with input and args",
			Input: `[
				{ name: wasm,  string: main.wasm },
				{ name: input, string: 'hello world' },
				{ name: args,  array: ['-flag'] },
			]`,
			Expected: Parameters{
				Build: false,
				Wasm:  "main.wasm",
				Input: "hello world",
				Args:  []string{"-flag"},
			},
		},
		{
			Name: "full build with input and args",
			Input: `[
				{ name: build,  string: 1 },
				{ name: input, string: 'hello world' },
				{ name: args,  array: ['-flag'] },
			]`,
			Expected: Parameters{
				Build: true,
				Wasm:  "",
				Input: "hello world",
				Args:  []string{"-flag"},
			},
		},
		{
			Name: "secret refs",
			Input: `[
				{ name: build,  string: 1 },
        { name: refs, map: { password: { secret: secret, key: key } } }
      ]`,
			Expected: Parameters{
				Build: true,
				Refs: map[string]Ref{
					"password": {Secret: "secret", Key: "key"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var actual Parameters

			if tc.Error != "" {
				require.EqualError(t, actual.UnmarshalText([]byte(tc.Input)), tc.Error)
				return
			}

			require.NoError(t, actual.UnmarshalText([]byte(tc.Input)))
			require.Equal(t, tc.Expected, actual)
		})
	}
}
