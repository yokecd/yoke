package wasi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasm"
)

type ExecParams struct {
	Module  *Module
	BinName string
	Stdin   io.Reader
	Stderr  io.Writer
	Args    []string
	Timeout time.Duration
	Env     map[string]string

	CompileParams
}

func Execute(ctx context.Context, params ExecParams) (output []byte, err error) {
	mod, closeModule, err := func() (*Module, func(context.Context) error, error) {
		if params.Module != nil {
			// If the module was passed via params, we do not own its lifetime and so do not close.
			return params.Module, func(context.Context) error { return nil }, nil
		}

		mod, err := Compile(ctx, params.CompileParams)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compile module: %w", err)
		}

		return &mod, mod.Close, nil
	}()
	if err != nil {
		return nil, err
	}
	defer func() {
		err = xerr.Join(err, closeModule(ctx))
	}()

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	defer func() {
		if params.Stderr == nil || len(stderr.Bytes()) == 0 {
			return
		}
		params.Stderr.Write([]byte("---\n"))
	}()

	moduleCfg := wazero.
		NewModuleConfig().
		WithName("").
		WithStdout(&stdout).
		WithStderr(func() io.Writer {
			if params.Stderr != nil {
				return io.MultiWriter(params.Stderr, &stderr)
			}
			return &stderr
		}()).
		WithRandSource(rand.Reader).
		WithSysNanosleep().
		WithSysNanotime().
		WithSysWalltime().
		WithArgs(append([]string{params.BinName}, params.Args...)...)

	if stdin := params.Stdin; stdin != nil {
		moduleCfg = moduleCfg.WithStdin(stdin)
	}

	for key, value := range params.Env {
		moduleCfg = moduleCfg.WithEnv(key, value)
	}

	defer internal.DebugTimer(ctx, "execute wasm module")()

	ctx, cancel := func() (context.Context, context.CancelFunc) {
		if params.Timeout < 0 {
			return ctx, func() {}
		}
		timeout := cmp.Or(params.Timeout, 10*time.Second)
		return context.WithTimeoutCause(
			ctx,
			timeout,
			fmt.Errorf("execution timeout (%s) exceeded", timeout.Abs().Round(time.Millisecond)),
		)
	}()
	defer cancel()

	if err := mod.Instantiate(ctx, moduleCfg); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			if cause := context.Cause(ctx); !errors.Is(cause, context.DeadlineExceeded) {
				return nil, fmt.Errorf("%v: %w", err, cause)
			}
			return nil, err
		}
		if params.Stderr != nil {
			return nil, fmt.Errorf("failed to instantiate module: %w", err)
		}
		details := stderr.String()
		if details == "" {
			details = "(no output captured on stderr)"
		}
		return nil, fmt.Errorf("failed to instantiate module: %w: stderr: %s", err, details)
	}

	return stdout.Bytes(), nil
}

type CompileParams struct {
	Wasm            []byte
	CacheDir        string
	MaxMemoryMib    uint32
	HostFunctionMap map[string]any
}

type Module struct {
	wazero.CompiledModule
	wazero.Runtime
	maxMemoryMib uint32
	checksum     string
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
	return xerr.Join(
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

const (
	maxPages = 1 << 16
	mibPages = 16
)

func Compile(ctx context.Context, params CompileParams) (Module, error) {
	defer internal.DebugTimer(ctx, "wasm compile")()

	cfg := wazero.
		NewRuntimeConfig().
		WithCloseOnContextDone(true)

	if params.MaxMemoryMib > 0 {
		cfg = cfg.WithMemoryLimitPages(min(mibPages*params.MaxMemoryMib, maxPages))
	}

	if params.CacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(params.CacheDir)
		if err != nil {
			return Module{}, fmt.Errorf("failed to instantiate compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, cfg)

	hostModule := runtime.NewHostModuleBuilder("host")

	for name, fn := range params.HostFunctionMap {
		hostModule = hostModule.NewFunctionBuilder().WithFunc(fn).Export(name)
	}

	if _, err := hostModule.Instantiate(ctx); err != nil {
		return Module{}, fmt.Errorf("failed to instantiate host module: %w", err)
	}

	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	mod, err := runtime.CompileModule(ctx, params.Wasm)
	if err != nil {
		return Module{}, err
	}

	return Module{
		Runtime:        runtime,
		CompiledModule: mod,
		maxMemoryMib:   params.MaxMemoryMib,
		checksum:       internal.SHA1HexString(params.Wasm),
	}, nil
}

func (mod Module) MaxMemoryMib() uint32 {
	return mod.maxMemoryMib
}

func (mod Module) Checksum() string {
	return mod.checksum
}

func Error(ctx context.Context, module api.Module, ptr wasm.Ptr, state wasm.State, err string) wasm.Buffer {
	mem := module.Memory()
	if !mem.WriteUint32Le(uint32(ptr), uint32(cmp.Or(state, wasm.StateError))) {
		panic("write state error out of memory range")
	}
	return Malloc(ctx, module, []byte(err))
}

func Malloc(ctx context.Context, module api.Module, data []byte) wasm.Buffer {
	results, err := module.ExportedFunction("malloc").Call(ctx, uint64(len(data)))
	if err != nil {
		panic(err)
	}
	buffer := wasm.Buffer(results[0])
	if !module.Memory().Write(buffer.Address(), data) {
		panic("write to memory out of range")
	}
	return buffer
}

func MallocJSON(ctx context.Context, module api.Module, ref wasm.Ptr, value any) wasm.Buffer {
	data, err := json.Marshal(value)
	if err != nil {
		return Error(ctx, module, ref, wasm.StateError, err.Error())
	}
	return Malloc(ctx, module, data)
}

func LoadString(module api.Module, value wasm.String) string {
	return string(LoadBytes(module, wasm.Buffer(value)))
}

func LoadBytes(module api.Module, value wasm.Buffer) []byte {
	if value == 0 {
		return nil
	}
	data, ok := module.Memory().Read(value.Address(), value.Length())
	if !ok {
		panic("memory read out of bounds")
	}
	return data
}
