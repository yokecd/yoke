package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/gob"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/yoke"
)

const (
	cacheRoot = "/.cache"
)

type CompilationMetadata struct {
	Data     []byte
	Checksum []byte
	Deadline time.Time
}

func LoadModule(ctx context.Context, path string) (*wasi.Module, error) {
	defer internal.DebugTimer(ctx, "Loading module")()

	uri, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if uri.Scheme == "" {
		path = filepath.Clean(path)
	}

	key := internal.SHA1HexString([]byte(path))

	compilationCache := filepath.Join(cacheRoot, key)
	if err := os.MkdirAll(compilationCache, 0755); err != nil {
		return nil, fmt.Errorf("failed to ensure compilation cache: %w", err)
	}

	metafilepath := filepath.Join(cacheRoot, key+".meta")

	mf, err := os.OpenFile(metafilepath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	// TODO: Handle error?
	defer mf.Close()

	fd := int(mf.Fd())

	buildFromCache := func() (module *wasi.Module, err error) {
		if err := unix.Flock(fd, unix.LOCK_SH); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		defer func() {
			if unlockErr := unix.Flock(fd, unix.LOCK_UN); unlockErr != nil {
				err = xerr.MultiErrFrom("", err, fmt.Errorf("failed to release lock: %w", err))
			}
		}()

		cacheFile, err := ReadMetaFile(metafilepath)
		if err != nil {
			return nil, fmt.Errorf("failed to read cache meta file: %w", err)
		}

		if cacheFile == nil {
			return nil, nil
		}

		mod, err := wasi.Compile(ctx, wasi.CompileParams{
			Wasm:     cacheFile.Data,
			CacheDir: compilationCache,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to compile from cache: %w", err)
		}

		return &mod, nil
	}

	buildFromSource := func() (*wasi.Module, error) {
		if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		defer unix.Flock(fd, unix.LOCK_UN)

		cacheFile, err := ReadMetaFile(metafilepath)
		if err != nil {
			return nil, fmt.Errorf("failed to read cache meta file: %w", err)
		}

		if cacheFile != nil {
			return nil, nil
		}

		wasm, err := yoke.LoadWasm(ctx, path, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load wasm: %w", err)
		}

		module, err := wasi.Compile(ctx, wasi.CompileParams{
			Wasm:     wasm,
			CacheDir: compilationCache,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to compile wasm: %w", err)
		}

		cachedFile := CompilationMetadata{
			Data:     wasm,
			Checksum: internal.SHA1(wasm),
			Deadline: time.Now().Add(time.Hour),
		}

		gw := gzip.NewWriter(mf)
		defer gw.Close()

		if err := gob.NewEncoder(gw).Encode(cachedFile); err != nil {
			return nil, fmt.Errorf("failed to encode meta to cache: %w", err)
		}

		return &module, nil
	}

	for {
		mod, err := buildFromCache()
		if err != nil {
			return nil, fmt.Errorf("failed to build module from cache: %w", err)
		}
		if mod != nil {
			return mod, nil
		}

		mod, err = buildFromSource()
		if err != nil {
			return nil, fmt.Errorf("failed to build module from source: %w", err)
		}
		if mod == nil {
			continue
		}
		return mod, nil
	}
}

func ReadMetaFile(filename string) (*CompilationMetadata, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if len(content) == 0 {
		return nil, nil
	}

	gr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, nil
	}
	defer gr.Close()

	var cacheFile CompilationMetadata
	if err := gob.NewDecoder(gr).Decode(&cacheFile); err != nil {
		return nil, fmt.Errorf("failed to decode cached file: %w", err)
	}

	if time.Now().After(cacheFile.Deadline) || !bytes.Equal(internal.SHA1(cacheFile.Data), cacheFile.Checksum) {
		// If the deadline is exceeded or the data corrupted, treat it is as cache miss.
		return nil, nil
	}

	return &cacheFile, nil
}
