package svr

import (
	"bytes"
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

	units "github.com/docker/go-units"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/internal/xhttp"
	"github.com/yokecd/yoke/pkg/yoke"
)

type Config struct {
	CacheFS string
}

func ConfigFromEnv() (cfg Config) {
	conf.Var(conf.Environ, &cfg.CacheFS, "YOKECD_CACHE_FS", conf.Default(os.TempDir()))
	conf.Environ.MustParse()
	return cfg
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

	const addr = ":3666"

	logger.Info("debug config",
		"cacheFS", cfg.CacheFS,
		"addr", addr,
	)

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

	mods := cache.NewModuleCache(cfg.CacheFS)

	svr := http.Server{
		Addr:    addr,
		Handler: Handler(mods, logger, client),
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
	MaxMemoryMib  uint32
	Timeout       time.Duration
}

type ExecResponse struct {
	Stdout json.RawMessage
	Stderr string
}

type HumanMemStats struct {
	TotalAlloc string
	Sys        string
	HeapAlloc  string
	HeapSys    string
	HeapIdle   string
	HeapInuse  string
	NextGC     string
}

func humanSize(value uint64) string {
	return units.HumanSize(float64(value))
}

func Handler(mods *cache.ModuleCache, logger *slog.Logger, client *k8s.Client) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /memstats", func(w http.ResponseWriter, r *http.Request) {
		runtime.GC()

		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)

		_ = json.NewEncoder(w).Encode(HumanMemStats{
			TotalAlloc: humanSize(stats.TotalAlloc),
			Sys:        humanSize(stats.Sys),
			HeapAlloc:  humanSize(stats.HeapAlloc),
			HeapSys:    humanSize(stats.HeapSys),
			HeapIdle:   humanSize(stats.HeapIdle),
			HeapInuse:  humanSize(stats.HeapInuse),
			NextGC:     humanSize(stats.NextGC),
		})
	})

	mux.HandleFunc("POST /exec", func(w http.ResponseWriter, r *http.Request) {
		var ex ExecuteReq
		if err := json.NewDecoder(r.Body).Decode(&ex); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mod, err := func() (*wasi.Module, error) {
			attrs := cache.ModuleAttrs{
				MaxMemoryMib:    ex.MaxMemoryMib,
				HostFunctionMap: host.BuildFunctionMap(client),
			}
			if len(ex.Source) > 0 {
				return mods.FromSource(r.Context(), ex.Source, attrs)
			}
			return mods.FromURL(r.Context(), ex.Path, attrs)
		}()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		output, _, err := yoke.EvalFlight(r.Context(), yoke.EvalParams{
			Client:        client,
			ClusterAccess: ex.ClusterAccess,
			Release:       ex.Release,
			Namespace:     ex.Namespace,
			Flight: yoke.FlightParams{
				Module:  yoke.Module{Instance: mod},
				Args:    ex.Args,
				Env:     ex.Env,
				Input:   strings.NewReader(ex.Input),
				Timeout: ex.Timeout,
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(json.RawMessage(output))
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := xhttp.WithLogger(logger, mux, nil)
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
