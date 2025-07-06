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

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/yoke"
)

const (
	annotationArgocdSyncWave string = "argocd.argoproj.io/sync-wave"
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

	client, err := k8s.NewClient(rest)
	if err != nil {
		return fmt.Errorf("failed to instantiate kubernetes clientset: %w", err)
	}

	secrets := make(map[string]string, len(cfg.Flight.Refs))
	for name, ref := range cfg.Flight.Refs {
		secret, err := client.Clientset.CoreV1().Secrets(cmp.Or(ref.Namespace, cfg.Namespace)).Get(ctx, ref.Secret, v1.GetOptions{})
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

		wasmPath, err := func() (string, error) {
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
		if err != nil {
			return nil, fmt.Errorf("failed to get wasm path: %w", err)
		}

		wasm, err := LoadWasm(ctx, wasmPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load wasm: %w", err)
		}

		mod, err := wasi.Compile(ctx, wasi.CompileParams{Wasm: wasm})
		if err != nil {
			return nil, fmt.Errorf("failed to compile module: %w", err)
		}
		defer mod.Close(ctx)

		data, _, err := yoke.EvalFlight(ctx, yoke.EvalParams{
			Client:   client,
			Release:  cfg.Application.Name,
			Matchers: nil,
			Flight: yoke.FlightParams{
				Module:    yoke.Module{Instance: &mod},
				Input:     strings.NewReader(cfg.Flight.Input),
				Args:      cfg.Flight.Args,
				Namespace: cfg.Application.Namespace,
				Env:       cfg.Env,
			},
		})

		return data, err
	}()
	if err != nil {
		return fmt.Errorf("failed to execute flight wasm: %w", err)
	}

	stages, err := internal.ParseStages(data)
	if err != nil {
		return fmt.Errorf("failed to parse output into valid flight output: %w", err)
	}

	addSyncWaveAnnotations(stages)

	internal.AddYokeMetadata(stages.Flatten(), cfg.Application.Name, cfg.Application.Namespace, "yokecd")

	return EncodeResources(json.NewEncoder(os.Stdout), stages.Flatten())
}

func addSyncWaveAnnotations(stages internal.Stages) {
	if len(stages) < 2 {
		return
	}

	for i, stage := range stages {
		for _, resource := range stage {
			annotations := resource.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}
			annotations[annotationArgocdSyncWave] = fmt.Sprint(i)
			resource.SetAnnotations(annotations)
		}
	}
}

func EncodeResources(out *json.Encoder, resources []*unstructured.Unstructured) error {
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
