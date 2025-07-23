package svr

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/davidmdm/conf"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xhttp"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/yoke"
)

type Config struct {
	CacheTTL                time.Duration
	CacheCollectionInterval time.Duration
}

func ConfigFromEnv() (cfg Config) {
	conf.Var(conf.Environ, &cfg.CacheTTL, "YOKECD_CACHE_TTL", conf.Default(24*time.Hour))
	conf.Var(conf.Environ, &cfg.CacheCollectionInterval, "YOKECD_CACHE_COLLECTION_INTERVAL", conf.Default(10*time.Second))
	conf.Environ.MustParse()
	return
}

func Run(ctx context.Context, cfg Config) (err error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	defer func() {
		if err != nil {
			logger.Error("program exiting with error", "error", err.Error())
		}
	}()

	if err := conf.Environ.Parse(); err != nil {
		return fmt.Errorf("failed to parse configuration: %w", err)
	}

	cfg.CacheCollectionInterval = max(cfg.CacheCollectionInterval, 1*time.Second)

	logger.Info("debug config", "cacheTTL", cfg.CacheTTL.String(), "cacheCollectionInterval", cfg.CacheCollectionInterval.String())

	mods := xsync.Map[string, *Mod]{}

	restCfg, err := func() (*rest.Config, error) {
		restcfg, err := rest.InClusterConfig()
		if err != nil {
			if !errors.Is(err, rest.ErrNotInCluster) {
				return nil, fmt.Errorf("failed to load kubernetes in-cluster config: %w", err)
			}
			restcfg, err = clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
		}
		return restcfg, err
	}()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	client, err := k8s.NewClient(restCfg)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	go func() {
		for range time.NewTicker(cfg.CacheCollectionInterval).C {
			var count int
			for key, mod := range mods.All() {
				func() {
					mod.Lock()
					defer mod.Unlock()
					if mod.Instance != nil && time.Now().After(mod.Deadline) {
						_ = mod.Instance.Close(ctx)
						mod.Instance = nil
						mods.Delete(key)
						count++
					}
				}()
			}
			if count > 0 {
				// Modules can consume a lot of ram. The Go runtime will not necessarily release the memory as soon as we stop referencing it.
				// In the interest of keeping RAM usage as low as possible as quick as possible, this is a good time to **hint** to the runtime
				// that this is a good time to release some memory.
				//
				// It may pause the world a couple milliseconds but that's an okay tradeoff.
				runtime.GC()

				logger.Info("cleared expired modules from cache", "count", count)
			}
		}
	}()

	svr := http.Server{
		Addr:    ":3666",
		Handler: Handler(cfg.CacheTTL, &mods, logger, client),
	}

	serverErr := make(chan error, 1)

	go func() {
		defer close(serverErr)
		if err := svr.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("failed to ListenAndServeTLS: %w", err)

	case <-ctx.Done():
	}

	logger.Info("shutting down YokeCD Server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return svr.Shutdown(ctx)
}

type Mod struct {
	sync.RWMutex
	Deadline time.Time
	Instance *wasi.Module
}

type ExecuteReq struct {
	Source        []byte
	ClusterAccess yoke.ClusterAccessParams
	Path          string
	Release       string
	Namespace     string
	Args          []string
	Env           map[string]string
	Input         string
}

type ExecResponse struct {
	Stdout json.RawMessage
	Stderr string
}

func Handler(ttl time.Duration, mods *xsync.Map[string, *Mod], logger *slog.Logger, client *k8s.Client) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /exec", func(w http.ResponseWriter, r *http.Request) {
		var ex ExecuteReq
		if err := json.NewDecoder(r.Body).Decode(&ex); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		key := func() string {
			if len(ex.Source) > 0 {
				return internal.SHA1HexString(ex.Source)
			}
			return ex.Path
		}()

		mod, _ := mods.LoadOrStore(key, new(Mod))

		cacheHit := true

		for {
			output, err := func() ([]byte, error) {
				mod.RLock()
				defer mod.RUnlock()

				if mod.Instance == nil || (ttl > 0 && time.Now().After(mod.Deadline)) {
					return nil, nil
				}

				output, _, err := yoke.EvalFlight(r.Context(), yoke.EvalParams{
					Client:        client,
					ClusterAccess: ex.ClusterAccess,
					Release:       ex.Release,
					Namespace:     ex.Namespace,
					Flight: yoke.FlightParams{
						Module: yoke.Module{Instance: mod.Instance},
						Args:   ex.Args,
						Env:    ex.Env,
						Input:  strings.NewReader(ex.Input),
					},
				})
				return output, err
			}()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if len(output) > 0 {
				xhttp.AddRequestAttrs(r.Context(), slog.Bool("cacheHit", cacheHit))
				json.NewEncoder(w).Encode(json.RawMessage(output))
				return
			}

			cacheHit = false

			if err := func() error {
				mod.Lock()
				defer mod.Unlock()

				if mod.Instance != nil && time.Now().Before(mod.Deadline) {
					return nil
				}

				wasm, err := func() ([]byte, error) {
					if len(ex.Source) == 0 {
						return yoke.LoadWasm(r.Context(), ex.Path, false)
					}
					r, err := gzip.NewReader(bytes.NewReader(ex.Source))
					if err != nil {
						return nil, fmt.Errorf("invalid source: %w", err)
					}
					return io.ReadAll(r)
				}()
				if err != nil {
					return fmt.Errorf("failed to load wasm: %w", err)
				}

				instance, err := wasi.Compile(r.Context(), wasi.CompileParams{
					Wasm:           wasm,
					LookupResource: wasi.HostLookupResource(client),
				})
				if err != nil {
					return fmt.Errorf("failed to compile wasm: %w", err)
				}

				mod.Instance = &instance
				mod.Deadline = time.Now().Add(ttl)

				return nil
			}(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := xhttp.WithLogger(logger, mux)
	handler = xhttp.WithRecover(handler)

	return handler
}

func Exec(ctx context.Context, ex ExecuteReq) (json.RawMessage, error) {
	defer internal.DebugTimer(ctx, "http::exec")()

	data, err := json.Marshal(ex)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal execute request: %w", err)
	}

	req, err := http.NewRequest("POST", "http://localhost:3666/exec", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get response: %w", err)
	}
	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: %s", result)
	}

	return result, nil
}
