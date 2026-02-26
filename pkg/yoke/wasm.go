package yoke

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/oci"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
)

// LoadWasm serves to pull the flight params path and resolve its wasm on the flight params.
// It has no effect if the flight params:
// - Module is non-nil (used in pre-cached module calls)
// - path is empty (when stdin is used as desired output)
// - Wasm is non-empty
func LoadWasm(ctx context.Context, params *FlightParams) (err error) {
	if params.Module.Instance != nil || len(params.Wasm) > 0 || params.Path == "" {
		return nil
	}
	defer internal.DebugTimer(ctx, "load wasm")()

	params.Wasm, err = LoadWasmFromURL(ctx, params.Path, params.Insecure)
	return
}

func LoadWasmFromURL(ctx context.Context, path string, insecure bool) ([]byte, error) {
	uri, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path url: %w", err)
	}
	if uri.Scheme == "" || uri.Scheme == "file" {
		return loadFile(path)
	}

	if !slices.Contains([]string{"http", "https", "oci"}, uri.Scheme) {
		return nil, fmt.Errorf("unsupported protocol: %s - http(s) and oci supported only", uri.Scheme)
	}

	if uri.Scheme == "oci" {
		return oci.PullArtifact(ctx, oci.PullArtifactParams{
			URL:      uri.String(),
			Insecure: insecure,
		})
	}

	req, err := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get response: %w", err)
	}
	defer func() {
		err = xerr.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("unexpected statuscode fetching %s: %d", uri.String(), resp.StatusCode)
	}

	r := func() io.Reader {
		if resp.Header.Get("Content-Encoding") == "gzip" || strings.HasSuffix(req.URL.Path, ".gz") {
			return gzipReader(resp.Body)
		}
		return resp.Body
	}()

	return io.ReadAll(r)
}

func loadFile(path string) (result []byte, err error) {
	if filepath.Ext(path) != ".gz" {
		return os.ReadFile(path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = xerr.Join(err, file.Close())
	}()

	return io.ReadAll(gzipReader(file))
}

func gzipReader(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		gr, err := gzip.NewReader(r)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(pw, gr); err != nil {
			pw.CloseWithError(err)
		}
		pw.CloseWithError(gr.Close())
	}()

	return pr
}

type ClusterAccessParams = host.ClusterAccessParams

type EvalParams struct {
	Client        *k8s.Client
	Release       string
	Namespace     string
	ClusterAccess ClusterAccessParams
	Flight        FlightParams
}

func EvalFlight(ctx context.Context, params EvalParams) ([]byte, error) {
	if params.Flight.Input != nil && params.Flight.Path == "" && params.Flight.Module.Instance == nil && len(params.Flight.Wasm) == 0 {
		output, err := io.ReadAll(params.Flight.Input)
		return output, err
	}

	if err := LoadWasm(ctx, &params.Flight); err != nil {
		return nil, fmt.Errorf("failed to load wasm program: %w", err)
	}

	yokeEnvVars := map[string]string{
		"YOKE_RELEASE":   params.Release,
		"YOKE_NAMESPACE": params.Namespace,
		"NAMESPACE":      params.Namespace,
		"YOKE_VERSION":   internal.GetYokeVersion(),
	}

	env := map[string]string{}
	for _, vars := range []map[string]string{params.Flight.Env, yokeEnvVars} {
		maps.Copy(env, vars)
	}

	ctx = host.WithOwner(ctx, internal.OwnerFrom(params.Release, params.Namespace))
	ctx = host.WithClusterAccess(ctx, params.ClusterAccess)

	output, err := wasi.Execute(ctx, wasi.ExecParams{
		Module:  params.Flight.Module.Instance,
		BinName: params.Release,
		Stdin:   params.Flight.Input,
		Stderr:  params.Flight.Stderr,
		Args:    params.Flight.Args,
		Timeout: params.Flight.Timeout,
		Env:     env,
		CompileParams: wasi.CompileParams{
			Wasm:            params.Flight.Wasm,
			CacheDir:        params.Flight.CompilationCacheDir,
			HostFunctionMap: host.BuildFunctionMap(params.Client),
			MaxMemoryMib:    uint32(params.Flight.MaxMemoryMib),
		},
	})

	return output, err
}
