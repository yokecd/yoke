package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"path"
	"sync"
	"weak"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/yoke"
)

type CachedModule struct {
	Instance weak.Pointer[wasi.Module]
	mutex    sync.RWMutex
}

type ModuleCache struct {
	mods   *xsync.Map[string, *CachedModule]
	paths  *xsync.Map[string, *sync.Mutex]
	fsRoot string
}

func NewModuleCache(fsRoot string) *ModuleCache {
	return &ModuleCache{
		mods:   new(xsync.Map[string, *CachedModule]),
		paths:  new(xsync.Map[string, *sync.Mutex]),
		fsRoot: fsRoot,
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

func (cache *ModuleCache) FromSource(ctx context.Context, source []byte, attrs ModuleAttrs) (*wasi.Module, error) {
	key := internal.SHA1HexString(source)
	mod, _ := cache.mods.LoadOrStore(key, &CachedModule{mutex: sync.RWMutex{}})

	mod.mutex.Lock()
	defer mod.mutex.Unlock()

	if instance := mod.Instance.Value(); instance != nil && instance.MaxMemoryMib() == attrs.MaxMemoryMib {
		return instance, nil
	}

	gr, err := gzip.NewReader(bytes.NewReader(source))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	wasm, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("failed to load wasm module: %w", err)
	}

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

func (cache *ModuleCache) loadRemoteWASM(ctx context.Context, uri string) ([]byte, error) {
	mutex, _ := cache.paths.LoadOrStore(uri, new(sync.Mutex))

	mutex.Lock()
	defer mutex.Unlock()

	filepath := path.Join(cache.fsRoot, internal.SHA1HexString([]byte(uri)))

	data, err := os.ReadFile(filepath)
	if err == nil {
		return data, err
	}

	data, err = yoke.LoadWasm(ctx, uri, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load wasm: %w", err)
	}

	var compressed bytes.Buffer

	gw := gzip.NewWriter(&compressed)
	if _, err := gw.Write(data); err != nil {
		return nil, fmt.Errorf("failed to gzip wasm data: %w", err)
	}

	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	if err := os.WriteFile(filepath, compressed.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write gzip wasm to cache: %w", err)
	}

	return compressed.Bytes(), nil
}

func (cache *ModuleCache) FromURL(ctx context.Context, url string, attrs ModuleAttrs) (*wasi.Module, error) {
	if cachedMod, _ := cache.mods.Load(url); cachedMod != nil {
		instance := func() *wasi.Module {
			cachedMod.mutex.RLock()
			defer cachedMod.mutex.RUnlock()
			return cachedMod.Instance.Value()
		}()
		if instance != nil && instance.MaxMemoryMib() == attrs.MaxMemoryMib {
			return instance, nil
		}
	}

	data, err := cache.loadRemoteWASM(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to load remote wasm: %w", err)
	}

	module, err := cache.FromSource(ctx, data, attrs)
	if err != nil {
		return nil, err
	}

	cachedMod, _ := cache.mods.Load(internal.SHA1HexString(data))
	cache.mods.Store(url, cachedMod)

	return module, nil
}
