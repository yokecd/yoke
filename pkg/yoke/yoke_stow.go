package yoke

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/davidmdm/x/xcontainer"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/oci"
	"github.com/yokecd/yoke/internal/wasi"
)

type StowParams struct {
	WasmFile string
	URL      string
	Tags     []string
	Insecure bool
}

func Stow(ctx context.Context, params StowParams) error {
	wasm, err := loadFile(params.WasmFile)
	if err != nil {
		return fmt.Errorf("failed to load wasm file: %w", err)
	}

	if _, err := wasi.Compile(ctx, wasi.CompileParams{Wasm: wasm}); err != nil {
		return fmt.Errorf("invalid wasm module: %w", err)
	}

	sha256 := internal.SHA256HexString(wasm)

	tags := xcontainer.ToSet(params.Tags)
	tags.Add("sha256_"+sha256, "latest")

	if _, tag, _ := strings.Cut(path.Base(params.URL), ":"); tag != "" {
		tags.Add(tag)
	}

	digestURL, err := oci.PushArtifact(ctx, oci.PushArtifactParams{
		Data:     wasm,
		URL:      params.URL,
		Insecure: params.Insecure,
		Tags:     tags.Collect(),
	})
	if err != nil {
		return fmt.Errorf("failed to stow wasm artifact: %w", err)
	}

	return yaml.NewEncoder(internal.Stderr(ctx)).Encode(struct {
		DigestURL string   `yaml:"digestUrl"`
		ModuleSHA string   `yaml:"moduleSHA"`
		Tags      []string `yaml:"tags"`
	}{digestURL, sha256, slices.Sorted(tags.All())})
}
