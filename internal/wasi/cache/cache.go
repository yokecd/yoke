package cache

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"sync"
	"weak"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xcrypto"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/yoke"
)

type CachedModule struct {
	Instance weak.Pointer[wasi.Module]
	mutex    sync.RWMutex
}

type ModuleCache struct {
	mods  *xsync.Map[string, *CachedModule]
	paths *xsync.Map[string, *sync.Mutex]

	fsRoot string
	Globs  internal.Globs
	Keys   xcrypto.PublicKeySet
}

func NewModuleCache(fsRoot string, globs internal.Globs, keys xcrypto.PublicKeySet) *ModuleCache {
	return &ModuleCache{
		mods:   new(xsync.Map[string, *CachedModule]),
		paths:  new(xsync.Map[string, *sync.Mutex]),
		fsRoot: fsRoot,
		Globs:  globs,
		Keys:   keys,
	}
}

type ModuleAttrs struct {
	MaxMemoryMib    uint32
	HostFunctionMap map[string]any
}

func (cache *ModuleCache) All() iter.Seq[*wasi.Module] {
	return func(yield func(*wasi.Module) bool) {
		seen := map[*wasi.Module]struct{}{}
		for _, ptr := range cache.mods.All() {
			if instance := ptr.Instance.Value(); instance != nil {
				if _, ok := seen[instance]; ok {
					continue
				}
				seen[instance] = struct{}{}
				if !yield(instance) {
					return
				}
			}
		}
	}
}

func (cache *ModuleCache) FromSource(ctx context.Context, wasm []byte, attrs ModuleAttrs) (*wasi.Module, error) {
	key := internal.SHA1HexString(wasm)
	mod, _ := cache.mods.LoadOrStore(key, &CachedModule{mutex: sync.RWMutex{}})
	if instance := mod.Instance.Value(); instance != nil && instance.MaxMemoryMib() == attrs.MaxMemoryMib {
		return instance, nil
	}

	mod.mutex.Lock()
	defer mod.mutex.Unlock()

	instance, err := wasi.Compile(ctx, wasi.CompileParams{
		Wasm:            wasm,
		CacheDir:        cache.fsRoot,
		MaxMemoryMib:    attrs.MaxMemoryMib,
		HostFunctionMap: attrs.HostFunctionMap,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to compile module: %w", err)
	}

	mod.Instance = weak.Make(&instance)

	return &instance, nil
}

type ErrDisallowedModule string

func (err ErrDisallowedModule) Error() string {
	return string(err)
}

func (ErrDisallowedModule) Is(err error) bool {
	_, ok := err.(ErrDisallowedModule)
	return ok
}

func IsDisallowedModuleError(err error) bool {
	return errors.Is(err, ErrDisallowedModule(""))
}

func (cache *ModuleCache) loadWasm(ctx context.Context, uri string, insecure bool) ([]byte, error) {
	data, err := os.ReadFile(cache.fsPath(uri))
	if err == nil {
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader for cached wasm file: %w", err)
		}
		return io.ReadAll(gr)
	}

	data, err = yoke.LoadWasmFromURL(ctx, uri, insecure)
	if err != nil {
		return nil, fmt.Errorf("failed to load wasm: %w", err)
	}

	return data, nil
}

type FromURLParams struct {
	URL      string
	Checksum string
	Insecure bool
	Attrs    ModuleAttrs
}

func (cache *ModuleCache) FromURL(ctx context.Context, params FromURLParams) (*wasi.Module, error) {
	if mod := cache.pullFromCache(params.URL, params.Attrs); mod != nil {
		return mod, nil
	}

	if !cache.Globs.Match(params.URL) {
		return nil, ErrDisallowedModule(fmt.Sprintf("module %q not allowed", params.URL))
	}

	mutex, _ := cache.paths.LoadOrStore(params.URL, new(sync.Mutex))

	mutex.Lock()
	defer mutex.Unlock()

	if mod := cache.pullFromCache(params.URL, params.Attrs); mod != nil {
		return mod, nil
	}

	wasm, err := cache.loadWasm(ctx, params.URL, params.Insecure)
	if err != nil {
		return nil, fmt.Errorf("failed to load remote wasm: %w", err)
	}

	if expected := cmp.Or(params.Checksum, internal.ChecksumFromPath(params.URL)); expected != "" {
		if actual := internal.SHA256HexString(wasm); actual != expected {
			return nil, fmt.Errorf("failed to validate checksum for module: expected %q but got %q", expected, actual)
		}
	}

	if len(cache.Keys) > 0 && (len(cache.Globs) == 0 || !cache.Globs.Match(params.URL)) {
		if err := xcrypto.VerifyModule(cache.Keys, wasm); err != nil {
			return nil, fmt.Errorf("failed to verify module: %w", err)
		}
	}

	if err := cache.toDisk(params.URL, wasm); err != nil {
		return nil, fmt.Errorf("failed to cache module on disk: %w", err)
	}

	module, err := cache.FromSource(ctx, wasm, params.Attrs)
	if err != nil {
		return nil, err
	}

	cachedMod, _ := cache.mods.Load(internal.SHA1HexString(wasm))
	cache.mods.Store(params.URL, cachedMod)

	return module, nil
}

func (cache ModuleCache) fsPath(uri string) string {
	return filepath.Join(cache.fsRoot, internal.SHA1HexString([]byte(uri)))
}

func (cache ModuleCache) toDisk(url string, wasm []byte) error {
	var (
		compressed bytes.Buffer
		gw         = gzip.NewWriter(&compressed)
	)
	if _, err := gw.Write(wasm); err != nil {
		return fmt.Errorf("failed to gzip wasm data: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}
	if err := os.WriteFile(cache.fsPath(url), compressed.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write gzip wasm to cache: %w", err)
	}
	return nil
}

func (cache ModuleCache) pullFromCache(key string, attrs ModuleAttrs) *wasi.Module {
	if cachedMod, _ := cache.mods.Load(key); cachedMod != nil {
		instance := func() *wasi.Module {
			cachedMod.mutex.RLock()
			defer cachedMod.mutex.RUnlock()
			return cachedMod.Instance.Value()
		}()
		if instance != nil && instance.MaxMemoryMib() == attrs.MaxMemoryMib {
			return instance
		}
	}
	return nil
}
