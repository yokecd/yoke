package wasi

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"reflect"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
)

type ExecParams struct {
	Wasm     []byte
	Module   *Module
	Release  string
	Stdin    io.Reader
	Args     []string
	Env      map[string]string
	CacheDir string
}

func Execute(ctx context.Context, params ExecParams) (output []byte, err error) {
	mod, closeModule, err := func() (*Module, func(context.Context) error, error) {
		if params.Module != nil {
			// If the module was passed via params, we do not own its lifetime and so do not close.
			return params.Module, func(context.Context) error { return nil }, nil
		}

		mod, err := Compile(ctx, CompileParams{
			Wasm:     params.Wasm,
			CacheDir: params.CacheDir,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compile module: %w", err)
		}

		return &mod, mod.Close, nil
	}()
	if err != nil {
		return nil, err
	}
	defer func() {
		err = xerr.MultiErrFrom("", err, closeModule(ctx))
	}()

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

	defer internal.DebugTimer(ctx, "execute wasm module")()

	if err := mod.Instantiate(ctx, moduleCfg); err != nil {
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

type Module struct {
	wazero.CompiledModule
	wazero.Runtime
}

func (mod Module) Instantiate(ctx context.Context, cfg wazero.ModuleConfig) error {
	module, err := mod.InstantiateModule(ctx, mod.CompiledModule, cfg)
	if err != nil {
		return err
	}
	if !reflect.ValueOf(module).IsNil() {
		if err := module.Close(ctx); err != nil {
			return fmt.Errorf("failed to close module: %w", err)
		}
	}
	return nil
}

func (mod Module) Close(ctx context.Context) error {
	return xerr.MultiErrFrom("",
		func() error {
			if mod.CompiledModule == nil {
				return nil
			}
			return mod.CompiledModule.Close(ctx)
		}(),
		func() error {
			if mod.Runtime == nil {
				return nil
			}
			return mod.Runtime.Close(ctx)
		}(),
	)
}

func Compile(ctx context.Context, params CompileParams) (Module, error) {
	defer internal.DebugTimer(ctx, "wasm compile")()

	cfg := wazero.
		NewRuntimeConfig().
		WithCloseOnContextDone(true)

	if params.CacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(params.CacheDir)
		if err != nil {
			return Module{}, fmt.Errorf("failed to instantiate compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, cfg)

	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	mod, err := runtime.CompileModule(ctx, params.Wasm)
	if err != nil {
		return Module{}, err
	}

	return Module{Runtime: runtime, CompiledModule: mod}, nil
}
