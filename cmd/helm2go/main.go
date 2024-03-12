package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"

	"github.com/davidmdm/ansi"
	"github.com/davidmdm/x/xcontext"
	"github.com/davidmdm/yoke/internal/home"
	"github.com/davidmdm/yoke/pkg/helm"
)

var (
	cache          = filepath.Join(home.Dir, ".cache/yoke")
	schemaGenDir   = filepath.Join(cache, "readme-generator-for-helm")
	flightTemplate *template.Template
)

//go:embed flight.go.tpl
var ft string

func init() {
	tpl, err := template.New("").Parse(ft)
	if err != nil {
		panic(err)
	}
	flightTemplate = tpl
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	repo := flag.String("repo", "", "bitnami repo to turn into a flight function")
	useSchema := flag.Bool("schema", false, "prefer schema over parsing values file")
	outDir := flag.String("outdir", "", "outdir for the flight package")

	flag.Parse()

	if *repo == "" {
		return fmt.Errorf("-repo is required")
	}
	if *outDir == "" {
		return fmt.Errorf("-outdir is required")
	}

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT)
	defer cancel()

	if err := ensureReadmeGenerator(ctx); err != nil {
		return fmt.Errorf("failed to ensure bitnami/readme-generator installation: %w", err)
	}

	if err := ensureGoJsonSchema(ctx); err != nil {
		return fmt.Errorf("failed to ensure go-jsonschema installation: %w", err)
	}

	*outDir, _ = filepath.Abs(*outDir)
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create outdir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "helm", "pull", *repo)
	if err := x(cmd, WithDir(*outDir)); err != nil {
		return fmt.Errorf("failed to pull helm repo: %w", err)
	}

	entries, err := os.ReadDir(*outDir)
	if err != nil {
		return fmt.Errorf("failed to read outdir: %w", err)
	}

	var archive string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tgz" {
			archive = filepath.Join(*outDir, entry.Name())
		}
	}

	archiveData, err := os.ReadFile(archive)
	if err != nil {
		return err
	}

	chart, err := helm.LoadChartFromZippedArchive(archiveData)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	// schemaFile must be called values for the generation to use: type Values
	schemaFile := filepath.Join(os.TempDir(), "values")
	valuesFile := filepath.Join(os.TempDir(), "raw")

	err = func() error {
		if *useSchema {
			if len(chart.Schema) > 0 {
				fmt.Println("using charts builtin schema")
				if err := os.WriteFile(schemaFile, chart.Schema, 0o644); err != nil {
					return fmt.Errorf("failed to write schema to temp file: %w", err)
				}
				return nil
			}
			fmt.Println("schema not found in chart")
		}

		fmt.Println("inferring schema from values file")
		if err := os.WriteFile(valuesFile, chart.Values, 0o644); err != nil {
			return fmt.Errorf("failed to write values to temp file: %w", err)
		}

		genSchema := exec.CommandContext(ctx, "node", "./bin/index.js", "-v", valuesFile, "-s", schemaFile)
		if err := x(genSchema, WithDir(schemaGenDir)); err != nil {
			return fmt.Errorf("failed to generate jsonschema: %w", err)
		}

		return nil
	}()
	if err != nil {
		return fmt.Errorf("failed create schema: %w", err)
	}

	packageName := filepath.Base(*outDir)

	genGoTypes := exec.CommandContext(ctx, "go-jsonschema", schemaFile, "-o", filepath.Join(*outDir, "values.go"), "-p", packageName, "--only-models")
	if err := x(genGoTypes); err != nil {
		return fmt.Errorf("failed to gen go types: %w", err)
	}

	flight, err := os.Create(filepath.Join(*outDir, "flight.go"))
	if err != nil {
		return err
	}
	defer flight.Close()

	return flightTemplate.Execute(flight, struct {
		Archive string
		Package string
	}{
		Archive: filepath.Base(archive),
		Package: packageName,
	})
}

func ensureReadmeGenerator(ctx context.Context) error {
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fmt.Errorf("failed to ensure yoke cache: %w", err)
	}

	if _, err := os.Stat(schemaGenDir); err != nil {
		clone := exec.CommandContext(ctx, "git", "clone", "https://github.com/bitnami/readme-generator-for-helm")
		if err := x(clone, WithDir(cache)); err != nil {
			return fmt.Errorf("failed to clone schema generator: %w", err)
		}

		downloadDeps := exec.CommandContext(ctx, "npm", "install")
		if err := x(downloadDeps, WithDir(schemaGenDir)); err != nil {
			return fmt.Errorf("failed to download schema generator dependencies: %w", err)
		}
	}

	return nil
}

func ensureGoJsonSchema(ctx context.Context) error {
	if x(exec.CommandContext(ctx, "command", "-v", "go-jsonschema")) == nil {
		return nil
	}
	if err := x(exec.CommandContext(ctx, "go", "install", "github.com/atombender/go-jsonschema@latest")); err != nil {
		return fmt.Errorf("failed to install go-jsonschema: %w", err)
	}

	return nil
}

var cyan = ansi.MakeStyle(ansi.FgCyan).Sprint

func x(cmd *exec.Cmd, opts ...XOpt) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	for _, apply := range opts {
		apply(cmd)
	}

	fmt.Println()
	fmt.Println("running:", cyan(strings.Join(cmd.Args, " ")))
	fmt.Println()

	return cmd.Run()
}

type XOpt func(*exec.Cmd)

func WithDir(dir string) XOpt {
	return func(c *exec.Cmd) {
		c.Dir = dir
	}
}
