package wasi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"reflect"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/davidmdm/x/xerr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasm"
)

type ExecParams struct {
	Wasm     []byte
	Module   *Module
	Release  string
	Stdin    io.Reader
	Args     []string
	Env      map[string]string
	CacheDir string
	Client   *k8s.Client
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
			Client:   params.Client,
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
	Client   *k8s.Client
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

	hostModule := runtime.NewHostModuleBuilder("host")

	for name, fn := range map[string]any{
		"k8s_lookup": func(ctx context.Context, module api.Module, stateRef wasm.Ptr, name, namespace, kind, apiVersion wasm.String) wasm.Buffer {
			if params.Client == nil {
				return wasm.Error(ctx, module, stateRef, wasm.StateFeatureNotGranted, "")
			}

			gv, err := schema.ParseGroupVersion(apiVersion.Load(module))
			if err != nil {
				return wasm.Error(ctx, module, stateRef, wasm.StateError, err.Error())
			}

			mapping, err := params.Client.Mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind.Load(module)}, gv.Version)
			if err != nil {
				return wasm.Error(ctx, module, stateRef, wasm.StateError, err.Error())
			}

			intf := func() dynamic.ResourceInterface {
				intf := params.Client.Dynamic.Resource(mapping.Resource)
				if mapping.Scope == meta.RESTScopeNamespace {
					return intf.Namespace(cmp.Or(namespace.Load(module), "default"))
				}
				return intf
			}()

			resource, err := intf.Get(ctx, name.Load(module), metav1.GetOptions{})
			if err != nil {
				errState := func() wasm.State {
					switch {
					case kerrors.IsNotFound(err):
						return wasm.StateNotFound
					case kerrors.IsForbidden(err):
						return wasm.StateForbidden
					case kerrors.IsUnauthorized(err):
						return wasm.StateUnauthenticated
					default:
						return wasm.StateError
					}
				}()
				return wasm.Error(ctx, module, stateRef, errState, err.Error())
			}

			data, err := resource.MarshalJSON()
			if err != nil {
				return wasm.Error(ctx, module, stateRef, wasm.StateError, err.Error())
			}

			results, err := module.ExportedFunction("malloc").Call(ctx, uint64(len(data)))
			if err != nil {
				// if we cannot malloc, let's crash with gumption.
				panic(err)
			}

			buffer := wasm.Buffer(results[0])

			module.Memory().Write(buffer.Address(), data)

			return buffer
		},
	} {
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

	return Module{Runtime: runtime, CompiledModule: mod}, nil
}
