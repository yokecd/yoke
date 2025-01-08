package wasi

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
)

type ExecParams struct {
	Wasm           []byte
	CompiledModule wazero.CompiledModule
	Release        string
	Stdin          io.Reader
	Args           []string
	Env            map[string]string
	CacheDir       string
}

func Execute(ctx context.Context, params ExecParams) (output []byte, err error) {
	cfg := wazero.
		NewRuntimeConfig().
		WithCloseOnContextDone(true)

	if params.CacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(params.CacheDir)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer func() {
		err = xerr.MultiErrFrom("", err, runtime.Close(ctx))
	}()

	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	moduleCfg := wazero.
		NewModuleConfig().
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithRandSource(rand.Reader).
		WithSysNanosleep().
		WithSysNanotime().
		WithSysWalltime().
		WithArgs(append([]string{params.Release}, params.Args...)...)

	if stdin := params.Stdin; stdin != nil {
		moduleCfg = moduleCfg.WithStdin(stdin)
	}

	for key, value := range params.Env {
		moduleCfg = moduleCfg.WithEnv(key, value)
	}

	guest, teardown, err := func() (wazero.CompiledModule, func(ctx context.Context) error, error) {
		defer internal.DebugTimer(ctx, "compile wasm module")()

		if params.CompiledModule != nil {
			// Return a noop teardown since we do not own the compiledModule. Let the caller close it when they are ready.
			return params.CompiledModule, func(context.Context) error { return nil }, nil
		}

		guest, err := runtime.CompileModule(ctx, params.Wasm)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compile module: %w", err)
		}
		return guest, guest.Close, nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to compile module: %w", err)
	}
	defer teardown(ctx)

	defer internal.DebugTimer(ctx, "execute wasm module")()

	module, err := runtime.InstantiateModule(ctx, guest, moduleCfg)
	defer func() {
		if module != nil {
			err = xerr.MultiErrFrom("", err, module.Close(ctx))
		}
	}()
	if err != nil {
		details := stderr.String()
		if details == "" {
			details = "(no output captured on stderr)"
		}
		return nil, fmt.Errorf("failed to instantiate module: %w: stderr: %s", err, details)
	}

	return stdout.Bytes(), nil
}

type CompileParams struct {
	Wasm     []byte
	CacheDir string
}

func Compile(ctx context.Context, params CompileParams) (mod wazero.CompiledModule, err error) {
	defer internal.DebugTimer(ctx, "wasm compile")()

	cfg := wazero.
		NewRuntimeConfig().
		WithCloseOnContextDone(true)

	if params.CacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(params.CacheDir)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer func() {
		err = xerr.MultiErrFrom("", err, runtime.Close(ctx))
	}()

	return runtime.CompileModule(ctx, params.Wasm)
}
