package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"text/template"

	"github.com/davidmdm/ansi"
	"github.com/davidmdm/x/xcontext"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/pkg/helm"
)

var yellow = ansi.MakeStyle(ansi.FgYellow)

func debug(format string, args ...any) {
	yellow.Printf("\n"+format+"\n", args...)
}

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
	version := flag.String("version", "", "version of chart to download")
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

	if err := ensureGoLibrary(ctx, "go-jsonschema", "github.com/atombender/go-jsonschema@latest"); err != nil {
		return fmt.Errorf("failed to ensure go-jsonschema installation: %w", err)
	}
	if err := ensureGoLibrary(ctx, "gofumpt", "mvdan.cc/gofumpt@latest"); err != nil {
		return fmt.Errorf("failed to ensure gofumpt installation: %w", err)
	}
	if err := ensureGoLibrary(ctx, "goimports", "golang.org/x/tools/cmd/goimports@latest"); err != nil {
		return fmt.Errorf("failed to ensure goimports installation: %w", err)
	}

	packageName := regexp.MustCompile(`\W`).ReplaceAllString(filepath.Base(*outDir), "")
	*outDir, _ = filepath.Abs(*outDir)

	if *version != "" {
		*outDir = filepath.Join(*outDir, *version)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create outdir: %w", err)
	}

	if err := pullHelmRepo(ctx, *repo, *version, *outDir); err != nil {
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

	err = func() error {
		if len(chart.Schema) > 0 {
			debug("using charts builtin schema")
			if err := os.WriteFile(schemaFile, chart.Schema, 0o644); err != nil {
				return fmt.Errorf("failed to write schema to temp file: %w", err)
			}
		} else {
			if len(chart.Schema) == 0 {
				return errors.New("no schema found")
			}
		}

		genGoTypes := exec.CommandContext(ctx, "go-jsonschema", schemaFile, "-o", filepath.Join(*outDir, "values.go"), "-p", packageName, "--only-models")
		if err := x(genGoTypes); err != nil {
			return fmt.Errorf("failed to gen go types: %w", err)
		}

		return nil
	}()

	var useFallback bool
	if err != nil {
		debug("failed generate types from schema: %v", err)
		debug("fallbacking to map[string]any :'(")
		useFallback = true
	}

	flight, err := os.Create(filepath.Join(*outDir, "flight.go"))
	if err != nil {
		return err
	}
	defer flight.Close()

	return flightTemplate.Execute(flight, struct {
		Archive     string
		Package     string
		URL         string
		Version     string
		UseFallback bool
	}{
		URL:         *repo,
		Version:     getVersion(filepath.Base(archive)),
		Archive:     filepath.Base(archive),
		Package:     packageName,
		UseFallback: useFallback,
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
	} else {
		if err := x(exec.CommandContext(ctx, "git", "pull"), WithDir(filepath.Join(cache, "readme-generator-for-helm"))); err != nil {
			return fmt.Errorf("failed to pull schema generator: %w", err)
		}
	}

	return nil
}

func ensureGoLibrary(ctx context.Context, libraryName, libraryURI string) error {
	if err := x(exec.CommandContext(ctx, "go", "install", libraryURI)); err != nil {
		return fmt.Errorf("failed to install %s: %w", libraryName, err)
	}
	return nil
}

func pullHelmRepo(ctx context.Context, repo, version, out string) error {
	uri, err := url.Parse(repo)
	if err != nil {
		return err
	}
	if uri.Scheme == "" {
		uri.Scheme = "oci"
	}

	cmd := func() *exec.Cmd {
		switch uri.Scheme {
		case "http", "https":
			repo, chart := path.Split(uri.Path)
			uri.Path = repo
			return exec.CommandContext(ctx, "helm", "pull", "--repo", uri.String(), chart)
		default:
			return exec.CommandContext(ctx, "helm", "pull", uri.String())
		}
	}()

	if version != "" {
		cmd.Args = append(cmd.Args, "--version", version)
	}

	return x(cmd, WithDir(out))
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

func getVersion(archive string) string {
	archive = archive[:len(archive)-len(filepath.Ext(archive))]
	return archive[strings.LastIndex(archive, "-")+1:]
}
