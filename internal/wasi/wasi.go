package wasi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/davidmdm/x/xerr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasm"
)

type ExecParams struct {
	Wasm           []byte
	Module         *Module
	Release        string
	Stdin          io.Reader
	Stderr         io.Writer
	Args           []string
	Env            map[string]string
	CacheDir       string
	LookupResource HostLookupResourceFunc
}

func Execute(ctx context.Context, params ExecParams) (output []byte, err error) {
	mod, closeModule, err := func() (*Module, func(context.Context) error, error) {
		if params.Module != nil {
			// If the module was passed via params, we do not own its lifetime and so do not close.
			return params.Module, func(context.Context) error { return nil }, nil
		}

		mod, err := Compile(ctx, CompileParams{
			Wasm:           params.Wasm,
			CacheDir:       params.CacheDir,
			LookupResource: params.LookupResource,
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
		WithArgs(append([]string{params.Release}, params.Args...)...)

	if stdin := params.Stdin; stdin != nil {
		moduleCfg = moduleCfg.WithStdin(stdin)
	}

	for key, value := range params.Env {
		moduleCfg = moduleCfg.WithEnv(key, value)
	}

	ctx = withOwner(ctx, internal.OwnerFrom(params.Release, params.Env["YOKE_NAMESPACE"]))

	defer internal.DebugTimer(ctx, "execute wasm module")()

	if err := mod.Instantiate(ctx, moduleCfg); err != nil {
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
	Wasm           []byte
	CacheDir       string
	LookupResource HostLookupResourceFunc
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
			if params.LookupResource == nil {
				return Error(ctx, module, stateRef, wasm.StateFeatureNotGranted, "")
			}

			resource, err := params.LookupResource(
				ctx,
				LoadString(module, name),
				LoadString(module, namespace),
				LoadString(module, kind),
				LoadString(module, apiVersion),
			)
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
				return Error(ctx, module, stateRef, errState, err.Error())
			}

			data, err := resource.MarshalJSON()
			if err != nil {
				return Error(ctx, module, stateRef, wasm.StateError, err.Error())
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

func Error(ctx context.Context, module api.Module, ptr wasm.Ptr, state wasm.State, err string) wasm.Buffer {
	mem := module.Memory()
	mem.WriteUint32Le(uint32(ptr), uint32(cmp.Or(state, wasm.StateError)))
	return Malloc(ctx, module, []byte(err))
}

func Malloc(ctx context.Context, module api.Module, data []byte) wasm.Buffer {
	results, err := module.ExportedFunction("malloc").Call(ctx, uint64(len(data)))
	if err != nil {
		panic(err)
	}
	buffer := wasm.Buffer(results[0])
	module.Memory().Write(buffer.Address(), data)
	return buffer
}

func LoadString(module api.Module, value wasm.String) string {
	return string(LoadBytes(module, wasm.Buffer(value)))
}

func LoadBytes(module api.Module, value wasm.Buffer) []byte {
	if value == 0 {
		return nil
	}
	data, ok := module.Memory().Read(uint32(value>>32), uint32(value))
	if !ok {
		panic("memory read out of bounds")
	}
	return data
}

type HostLookupResourceFunc func(ctx context.Context, name, namespace, kind, apiVersion string) (*unstructured.Unstructured, error)

func HostLookupResource(client *k8s.Client, matchers []string) HostLookupResourceFunc {
	if client == nil {
		return nil
	}
	return func(ctx context.Context, name, namespace, kind, apiVersion string) (*unstructured.Unstructured, error) {
		gv, err := schema.ParseGroupVersion(apiVersion)
		if err != nil {
			return nil, err
		}

		mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version)
		if err != nil {
			return nil, err
		}

		intf := func() dynamic.ResourceInterface {
			intf := client.Dynamic.Resource(mapping.Resource)
			if mapping.Scope == meta.RESTScopeNamespace {
				return intf.Namespace(cmp.Or(namespace, "default"))
			}
			return intf
		}()

		resource, err := intf.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		for _, matcher := range matchers {
			if internal.MatchResource(resource, matcher) {
				return resource, nil
			}
		}

		if internal.GetOwner(resource) != getOwner(ctx) {
			return nil, kerrors.NewForbidden(schema.GroupResource{}, "", errors.New("cannot access resource outside of target release ownership"))
		}

		return resource, nil
	}
}

type ownerKey struct{}

func withOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

func getOwner(ctx context.Context) string {
	value, _ := ctx.Value(ownerKey{}).(string)
	return value
}
