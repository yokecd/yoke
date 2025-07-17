package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"
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
			Error: "invalid config: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go struct field CmpParam.array of type []string",
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

func TestConfigUnmarshallingInput(t *testing.T) {
	cases := []struct {
		Name  string
		Input string
		// empty string is a valid value in some tests
		ExpectedInput     *string
		ExpectedInputJson string
		Error             string
		Files             map[string]string
	}{
		{
			Name: "input string overrides other options",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: input, string: 'override' },
				{ name: inputFiles, array: ['values.yaml']}
			]`,
			ExpectedInput: ptr.To("override"),
		},
		{
			Name: "properly combines YAML and JSON files",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ['values.yaml', 'overrides.json']}
			]`,
			Files: map[string]string{
				"values.yaml":    "property: foo\nanother: baz",
				"overrides.json": `{ "another": "bar" }`,
			},
			ExpectedInputJson: `{ "another": "bar", "property": "foo" }`,
		},
		{
			Name: "can handle YAML anchors",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ['values.yaml']}
			]`,
			Files: map[string]string{
				"values.yaml": "property: &a foo\nanother: *a",
			},
			ExpectedInputJson: `{ "another": "foo", "property": "foo" }`,
		},
		{
			Name: "input map overrides input files",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ['values.yaml']},
				{ name: input, map: { 'another': 'baz' }}
			]`,
			Files: map[string]string{
				"values.yaml": "property: foo\nanother: bar",
			},
			ExpectedInputJson: `{ "another": "baz", "property": "foo" }`,
		},
		{
			Name: "empty input doesnt output anything to stdin",
			Input: `[
				{ name: wasm, string: main.wasm }
			]`,
			ExpectedInput: ptr.To(""),
		},
		{
			Name: "handles errors - not being able to read file",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ['values.yaml']}
			]`,
			Error: "invalid config: could not read file 'values.yaml': open values.yaml: no such file or directory",
		},
		{
			Name: "handles errors - not being able to parse YAML/JSON file",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ['values.yaml']}
			]`,
			Files: map[string]string{
				"values.yaml": `this is not a YAML file`,
			},
			Error: "invalid config: could not parse YAML or JSON file 'values.yaml': error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type map[string]interface {}",
		},
		{
			Name: "nested map overrides with empty input",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: input, map: { some: foo, "nested.foo": bar }}
			]`,
			ExpectedInputJson: `{ "nested": { "foo": "bar" }, "some": "foo" }`,
		},
		{
			Name: "nested map overrides with value files",
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ["values.yaml"] },
				{ name: input, map: { some: foo, "nested.foo": bar }}
			]`,
			Files: map[string]string{
				"values.yaml": `
property: bah
some: boo
nested:
  foo: bee
  bar: baz
`,
			},
			ExpectedInputJson: `{
        "nested": {
          "bar": "baz",
          "foo": "bar"
        },
        "property": "bah",
        "some": "foo"
      }`,
		},
		{
			Name: "complicated map overrides with value files",
			// tests overriding - existing value, new simple value, nested existing value, nested new object, existing array object, new array item, new object array...
			Input: `[
				{ name: wasm, string: main.wasm },
				{ name: inputFiles, array: ["values.yaml"] },
				{ name: input, map: { 
            some: foo, 
            new: bar,
            obj: "some: property\nanother: property",
            "nested.foo": bar, 
            "nested.new.bar": baz, 
            "nested.array.0.obj": value3, 
            "nested.array.-1": '{"some": "value"}',
            "nested.arrayStr.-1": baz ,
            "nested.newArrayObj.-1.foo": bar,
            "nested.bar": "some: inline\nyaml: true"
          }
        }
			]`,
			Files: map[string]string{
				"values.yaml": `
property: bah
some: boo
obj:
  foo: bar
nested:
  foo: bee
  bar: baz
  array:
    - obj: value
    - obj: value2
  arrayStr:
    - foo
    - bar
`,
			},
			ExpectedInputJson: `{
        "property": "bah",
        "new": "bar",
        "some": "foo",
        "obj": {
          "some": "property",
          "another": "property"
        },
        "nested": {
          "foo": "bar",
          "bar": {
            "some": "inline",
            "yaml": true
          },
          "new": {
            "bar": "baz"
          },
          "array": [
            { "obj": "value3" },
            { "obj": "value2" },
            { "some": "value" }
          ],
          "arrayStr": ["foo","bar","baz"],
          "newArrayObj": [
            { "foo": "bar" }
          ]
        }
      }`,
		},
		{
			Name: "input map with different types",
			// ArgoCD passes every parameter as a string, so we need to force the string to things that would be interpreted otherwise (bools, numbers, JSON/YAML strings..)
			Input: `[
        { name: wasm, string: main.wasm },
        { name: input, map: { 
            string: foo,
            number: "42",
            bool: "true",
            numberString: '"42"',
            boolString: '"true"',
            json: '{"some": "value", "num": 15, "bool": false, "numStr": "15", "boolStr": "false" }',
            yaml: "some: value\nnum: 15\nbool: false\nnumStr: '15'\nboolStr: 'false'\nnested:\n foo: bar\n array:\n - value"
          }
        }
      ]`,
			ExpectedInputJson: `{
        "string": "foo",
        "number": 42,
        "bool": true,
        "numberString": "42",
        "boolString": "true",
        "json": {
          "some": "value",
          "num": 15,
          "bool": false,
          "numStr": "15",
          "boolStr": "false"
        },
        "yaml": {
          "some": "value",
          "num": 15,
          "bool": false,
          "numStr": "15",
          "boolStr": "false",
          "nested": {
            "foo": "bar",
            "array": ["value"]
          }
        }
      }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			tmpDir := t.TempDir()
			origWD, _ := os.Getwd()

			for file, content := range tc.Files {
				tmpFile := filepath.Join(tmpDir, file)
				if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
					t.Error("error during test setup - creating fixtures", err)
					return
				}
			}

			if err := os.Chdir(tmpDir); err != nil {
				t.Error("error during test setup - chdir to temp", err)
			}
			defer func() {
				_ = os.Chdir(origWD)
			}()

			var actual Parameters

			if tc.Error != "" {
				require.EqualError(t, actual.UnmarshalText([]byte(tc.Input)), tc.Error)
				return
			}

			require.NoError(t, actual.UnmarshalText([]byte(tc.Input)))

			if tc.ExpectedInput != nil {
				require.Equal(t, *tc.ExpectedInput, actual.Input)
				return
			}
			require.JSONEq(t, tc.ExpectedInputJson, actual.Input)
		})
	}
}
