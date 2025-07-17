package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
)

// This program is not a valid flight, but is a program that will be used to test
// the yokecd plugin server as it does not deploy flights, only executes the wasm module.
func main() {
	flag.Parse()

	data, _ := io.ReadAll(os.Stdin)

	env := map[string]string{}
	for _, envvar := range os.Environ() {
		key, value, _ := strings.Cut(envvar, "=")
		env[key] = value
	}

	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"input": string(data),
		"env":   env,
		"args":  flag.Args(),
	})
}
