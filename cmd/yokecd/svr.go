package main

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
	"strings"
	"sync"
	"time"

	"github.com/davidmdm/conf"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/xhttp"
	"github.com/yokecd/yoke/internal/xsync"
	"github.com/yokecd/yoke/pkg/yoke"
)

func RunSvr(ctx context.Context) (err error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	defer func() {
		if err != nil {
			logger.Error("program exiting with error", "error", err.Error())
		}
	}()

	var ttl time.Duration
	conf.Var(conf.Environ, &ttl, "YOKECD_CACHE_TTL", conf.Default(24*time.Hour))

	if err := conf.Environ.Parse(); err != nil {
		return fmt.Errorf("failed to parse configuration: %w", err)
	}

	logger.Info("debug config", "ttl", ttl.String())

	mods := xsync.Map[string, *Mod]{}

	go func() {
		for range time.NewTicker(10 * time.Second).C {
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
				logger.Info("cleared expired modules from cache", "count", count)
			}
		}
	}()

	svr := http.Server{
		Addr:    ":3666",
		Handler: Handler(ttl, &mods, logger),
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
	Source    []byte            `json:"source"`
	Path      string            `json:"path"`
	Release   string            `json:"release"`
	Namespace string            `json:"namespace"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Input     string            `json:"input"`
}

type ExecResponse struct {
	Stdout json.RawMessage `json:"stdout"`
	Stderr string          `json:"stderr"`
}

func Handler(ttl time.Duration, mods *xsync.Map[string, *Mod], logger *slog.Logger) http.Handler {
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
					Client:   nil,
					Release:  ex.Release,
					Matchers: []string{},
					Flight: yoke.FlightParams{
						Module:    yoke.Module{Instance: mod.Instance},
						Args:      ex.Args,
						Env:       ex.Env,
						Namespace: ex.Namespace,
						Input:     strings.NewReader(ex.Input),
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

				instance, err := wasi.Compile(r.Context(), wasi.CompileParams{Wasm: wasm})
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
