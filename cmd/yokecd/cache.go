package main

import (
	"bytes"
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
	"github.com/yokecd/yoke/pkg/yoke"
)

const (
	cacheRoot    = "/.cache"
	wasmRoot     = cacheRoot + "/wasm"
	compiledRoot = cacheRoot + "/compiled"
)

func LoadWasm(ctx context.Context, path string) ([]byte, error) {
	uri, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if uri.Scheme == "" {
		return yoke.LoadWasm(ctx, path, false)
	}

	filename := filepath.Join(wasmRoot, internal.SHA1HexString([]byte(path)))

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	// TODO: Handle error?
	defer file.Close()

	fd := int(file.Fd())

	type CacheFile struct {
		Data     []byte
		Checksum []byte
		Deadline time.Time
	}

	for {
		data, err := func() (data []byte, err error) {
			if err := unix.Flock(fd, unix.LOCK_SH); err != nil {
				return nil, fmt.Errorf("failed to acquire lock: %w", err)
			}
			defer func() {
				if unlockErr := unix.Flock(fd, unix.LOCK_UN); unlockErr != nil {
					err = xerr.MultiErrFrom("", err, fmt.Errorf("failed to release lock: %w", err))
				}
			}()

			content, err := os.ReadFile(filename)
			if err != nil {
				return nil, err
			}

			if len(content) == 0 {
				return nil, nil
			}

			var cacheFile CacheFile
			if err := gob.NewDecoder(bytes.NewReader(content)).Decode(&cacheFile); err != nil {
				return nil, fmt.Errorf("failed to decode cached file: %w", err)
			}

			if time.Now().After(cacheFile.Deadline) || !bytes.Equal(internal.SHA1(cacheFile.Data), cacheFile.Checksum) {
				// If the deadline is exceeded or the data corrupted, treat it is as cache miss.
				return nil, nil
			}

			return cacheFile.Data, nil
		}()
		if err != nil {
			return nil, fmt.Errorf("failed to read from cache: %w", err)
		}

		if len(data) > 0 {
			return data, nil
		}

		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			if err == unix.EWOULDBLOCK {
				continue
			}
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		// Best effort, kernal will release lock once process ends.
		defer unix.Flock(fd, unix.LOCK_UN)

		wasm, err := yoke.LoadWasm(ctx, path, false)
		if err != nil {
			return nil, err
		}

		cachedFile := CacheFile{
			Data:     wasm,
			Checksum: internal.SHA1(wasm),
			Deadline: time.Now().Add(time.Hour),
		}

		if err := gob.NewEncoder(file).Encode(cachedFile); err != nil {
			return nil, fmt.Errorf("failed to encode data to cache: %w", err)
		}

		return wasm, nil
	}
}
