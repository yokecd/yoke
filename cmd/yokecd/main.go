package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"

	"github.com/davidmdm/x/xcontext"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	cfg, err := getConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	ctx = internal.WithDebugFlag(ctx, func(value bool) *bool { return &value }(true))

	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg Config) (err error) {
	defer internal.DebugTimer(ctx, fmt.Sprintf("evaluating application %s/%s", cfg.Application.Name, cfg.Flight.Wasm))()

	rest, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(rest)
	if err != nil {
		return fmt.Errorf("failed to instantiate kubernetes clientset: %w", err)
	}

	secrets := make(map[string]string, len(cfg.Flight.Refs))
	for name, ref := range cfg.Flight.Refs {
		secret, err := clientset.CoreV1().Secrets(cmp.Or(ref.Namespace, cfg.Namespace)).Get(ctx, ref.Secret, v1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get secret reference %q: %w", ref.Secret, err)
		}
		value, ok := secret.Data[ref.Key]
		if !ok {
			return fmt.Errorf("key %q not present in secret %q", ref.Key, ref.Secret)
		}
		secrets[name] = string(value)
	}

	data, err := func() ([]byte, error) {
		if cfg.Flight.Build {
			cfg.Flight.Wasm, err = Build()
			if err != nil {
				return nil, fmt.Errorf("failed to build binary: %w", err)
			}
			defer os.Remove(cfg.Flight.Wasm)
		}

		wasm, err := func() (string, error) {
			if !strings.HasPrefix(cfg.Flight.Wasm, "http://") && !strings.HasPrefix(cfg.Flight.Wasm, "https://") {
				return cfg.Flight.Wasm, nil
			}

			tpl, err := template.New("").Parse(cfg.Flight.Wasm)
			if err != nil {
				return "", fmt.Errorf("invalid template: %w", err)
			}

			tpl.Option("missingkey=error")

			var builder strings.Builder
			if err := tpl.Execute(&builder, secrets); err != nil {
				return "", fmt.Errorf("failed to execute template: %w", err)
			}

			return builder.String(), nil
		}()

		data, _, err := yoke.EvalFlight(ctx, cfg.Application.Name, yoke.FlightParams{
			Path:      wasm,
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
	var resources internal.List[*unstructured.Unstructured]
	if err := yaml.Unmarshal(data, &resources); err != nil {
		return fmt.Errorf("failed to unmarshal executed flight data: %w", err)
	}

	for _, resource := range resources {
		if err := out.Encode(resource); err != nil {
			return err
		}
	}

	return nil
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
