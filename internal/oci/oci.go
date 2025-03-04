package oci

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/davidmdm/x/xerr"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	gcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/yokecd/yoke/internal"
)

const (
	configMediaType = "application/vnd.yoke.config.v1+json"
	wasmMediaType   = "application/vnd.yoke.wasm.v1.gzip"
	ociScheme       = "oci://"
)

type PushArtfiactParams struct {
	URL      string
	Data     []byte
	Insecure bool
}

func PushArtifact(ctx context.Context, params PushArtfiactParams) (digestURL string, err error) {
	ociURL, ok := strings.CutPrefix(params.URL, ociScheme)
	if !ok {
		return "", fmt.Errorf("url must start with oci scheme: oci:// but got: %s", ociURL)
	}

	ref, err := name.ParseReference(ociURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse oci url: %w", err)
	}

	if ref.Identifier() == "" {
		return "", fmt.Errorf("artifact must be tagged")
	}

	compressed, err := gzipBuffer(params.Data)
	if err != nil {
		return "", fmt.Errorf("failed to gzip wasm data: %w", err)
	}

	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, configMediaType)

	layer := static.NewLayer(compressed, wasmMediaType)

	img, err = mutate.Append(img, mutate.Addendum{Layer: layer})
	if err != nil {
		return "", fmt.Errorf("failed to add layer to image: %w", err)
	}

	opts := []crane.Option{crane.WithContext(ctx)}
	if params.Insecure {
		opts = append(opts, crane.Insecure)
	}

	if err := crane.Push(img, ref.String(), opts...); err != nil {
		return "", err
	}

	digest, err := img.Digest()
	if err != nil {
		return "", fmt.Errorf("failed to get digest from image: %w", err)
	}

	return ociScheme + ref.Context().Digest(digest.String()).String(), nil
}

type PullArtifactParams struct {
	URL      string
	Insecure bool
}

func PullArtifact(ctx context.Context, params PullArtifactParams) (artifact []byte, err error) {
	ociURL, ok := strings.CutPrefix(params.URL, ociScheme)
	if !ok {
		return nil, fmt.Errorf("url must start with oci scheme: oci:// but got: %s", ociURL)
	}

	ref, err := name.ParseReference(ociURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse oci url: %w", err)
	}

	opts := []crane.Option{crane.WithContext(ctx)}
	if params.Insecure {
		opts = append(opts, crane.Insecure)
	}

	data, err := crane.Manifest(ref.String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest gcrv1.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	if manifest.Config.MediaType != configMediaType {
		return nil, fmt.Errorf("unexpected manifest media type got: %w", manifest.MediaType)
	}

	wasmLayer, ok := internal.Find(manifest.Layers, func(desc gcrv1.Descriptor) bool {
		return desc.MediaType == wasmMediaType
	})
	if !ok {
		return nil, fmt.Errorf("could not find wasm layer")
	}

	layer, err := crane.PullLayer(ref.Context().Name()+"@"+wasmLayer.Digest.String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to pull wasm layer: %w", err)
	}

	var closeErrs []error
	defer func() {
		if closeErr := xerr.MultiErrFrom("closing resources", closeErrs...); closeErr != nil {
			err = xerr.MultiErrFrom("", err, closeErr)
		}
	}()

	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("failed to get layer's data stream: %w", err)
	}
	defer func() { closeErrs = append(closeErrs, rc.Close()) }()

	gr, err := gzip.NewReader(rc)
	if err != nil {
		return nil, fmt.Errorf("unexpected layer content format: %w", err)
	}
	defer func() { closeErrs = append(closeErrs, gr.Close()) }()

	return io.ReadAll(gr)
}

func gzipBuffer(data []byte) (compressed []byte, err error) {
	var buffer bytes.Buffer

	gw := gzip.NewWriter(&buffer)

	if _, err := gw.Write(data); err != nil {
		return nil, err
	}

	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("closing gzip writer: %w", err)
	}

	return buffer.Bytes(), nil
}
