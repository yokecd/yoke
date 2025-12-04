package plugin

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/template"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/yokecd/yoke/cmd/yokecd/internal/svr"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
)

const annotationArgocdSyncWave string = "argocd.argoproj.io/sync-wave"

func Run(ctx context.Context, cfg Config) (err error) {
	defer internal.DebugTimer(ctx, fmt.Sprintf("evaluating application %s/%s", cfg.Application.Name, cfg.Flight.Wasm))()

	secrets, err := func() (map[string]string, error) {
		if len(cfg.Flight.Refs) == 0 {
			return nil, nil
		}

		rest, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in cluster config: %w", err)
		}

		client, err := k8s.NewClient(rest, "")
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate kubernetes clientset: %w", err)
		}

		secrets := make(map[string]string, len(cfg.Flight.Refs))
		for name, ref := range cfg.Flight.Refs {
			secret, err := client.Clientset.CoreV1().Secrets(cmp.Or(ref.Namespace, cfg.Namespace)).Get(ctx, ref.Secret, v1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to get secret reference %q: %w", ref.Secret, err)
			}
			value, ok := secret.Data[ref.Key]
			if !ok {
				return nil, fmt.Errorf("key %q not present in secret %q", ref.Key, ref.Secret)
			}
			secrets[name] = string(value)
		}
		return secrets, nil
	}()
	if err != nil {
		return fmt.Errorf("failed to lookup secret references: %w", err)
	}

	data, err := func() ([]byte, error) {
		if cfg.Flight.Build {
			cfg.Flight.Wasm, err = goBuild()
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

		uri, err := url.Parse(wasmPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse path: %w", err)
		}

		source, err := func() ([]byte, error) {
			if uri.Scheme != "" && uri.Scheme != "file" {
				return nil, nil
			}

			file, err := os.Open(wasmPath)
			if err != nil {
				return nil, fmt.Errorf("failed to open wasm file: %w", err)
			}
			defer file.Close()

			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)

			if _, err := io.Copy(gw, file); err != nil {
				return nil, fmt.Errorf("failed to gzip wasm: %w", err)
			}

			if err := gw.Close(); err != nil {
				return nil, fmt.Errorf("failed to gzip wasm: %w", err)
			}

			return buf.Bytes(), nil
		}()
		if err != nil {
			return nil, fmt.Errorf("failed to get source wasm: %w", err)
		}

		return svr.Exec(ctx, svr.ExecuteReq{
			Source:       source,
			Path:         wasmPath,
			Release:      cfg.Application.Name,
			Namespace:    cfg.Application.Namespace,
			Args:         cfg.Flight.Args,
			Env:          cfg.Env,
			Input:        cfg.Flight.Input,
			MaxMemoryMib: cfg.Flight.MaxMemoryMib,
			Timeout:      cfg.Flight.Timeout,
		})
	}()
	if err != nil {
		return fmt.Errorf("failed to execute flight wasm: %w", err)
	}

	stages, err := internal.ParseStages(data)
	if err != nil {
		return fmt.Errorf("failed to parse output into valid flight output: %w\n\nGot: %q", err, data)
	}

	addSyncWaveAnnotations(stages)

	internal.AddYokeMetadata(stages.Flatten(), cfg.Application.Name, cfg.Application.Namespace, "yokecd")

	return encodeResources(json.NewEncoder(internal.Stdout(ctx)), stages.Flatten())
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

func encodeResources(out *json.Encoder, resources []*unstructured.Unstructured) error {
	for _, resource := range resources {
		if err := out.Encode(resource); err != nil {
			return err
		}
	}

	return nil
}

func goBuild() (string, error) {
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
