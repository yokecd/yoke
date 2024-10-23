package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

func main() {
	cfg, err := getConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cfg Config) (err error) {
	data, err := func() ([]byte, error) {
		if cfg.Flight.Build {
			debug("building wasm")
			cfg.Flight.Wasm, err = Build()
			if err != nil {
				return nil, fmt.Errorf("failed to build binary: %w", err)
			}
			defer os.Remove(cfg.Flight.Wasm)
		}

		debug("loading wasm: %s", cfg.Flight.Wasm)

		data, _, err := yoke.EvalFlight(context.Background(), cfg.Application.Name, yoke.FlightParams{
			Path:      cfg.Flight.Wasm,
			Input:     strings.NewReader(cfg.Flight.Input),
			Args:      cfg.Flight.Args,
			Namespace: cfg.Application.Namespace,
		})

		return data, err
	}()
	if err != nil {
		return fmt.Errorf("failed to execute flight wasm: %w", err)
	}

	return EncodeResources(json.NewEncoder(os.Stdout), data)
}

func EncodeResources(out *json.Encoder, data []byte) error {
	debug("encoding resources")

	var resources internal.List[*unstructured.Unstructured]
	if err := yaml.Unmarshal(data, &resources); err != nil {
		return fmt.Errorf("failed to unmarshal executed flight data: %w", err)
	}

	for _, resource := range resources {
		debug("encoding: %s/%s", resource.GetKind(), resource.GetName())
		if err := out.Encode(resource); err != nil {
			return err
		}
	}

	debug("resources: %d", len(resources))

	return nil
}

func debug(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func Build() (string, error) {
	file, err := os.CreateTemp("", "main.*.wasm")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file to build wasm towards: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp wasm file: %w", err)
	}

	cmd := exec.Command("go", "build", "-o", file.Name(), ".")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	return file.Name(), cmd.Run()
}
