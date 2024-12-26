package main

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/wasi"
)

func Handler(locks *wasm.Locks, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("route not found: %s", r.URL.Path), http.StatusNotFound)
	})

	// no op liveness and readiness checks
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {})

	mux.HandleFunc("POST /crdconvert/{airway}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		airway := r.PathValue("airway")

		lock := locks.Get(airway)

		lock.Converter.RLock()
		defer lock.Converter.RUnlock()

		data, err := os.ReadFile(wasm.AirwayModulePath(airway, wasm.Converter))
		if err != nil {
			if kerrors.IsNotFound(err) {
				http.Error(w, "airway does not have converter module setup", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		resp, err := wasi.Execute(ctx, wasi.ExecParams{
			Wasm:     data,
			Stdin:    r.Body,
			Release:  "converter",
			CacheDir: wasm.AirwayModuleDir(airway),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if _, err := w.Write(resp); err != nil {
			logger.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	return withLogger(logger, mux)
}

func withLogger(logger *slog.Logger, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := statusWriter{ResponseWriter: w}

		handler.ServeHTTP(sw, r)

		logger.Info(
			"request served",
			"code", sw.Code(),
			"method", r.Method,
			"path", r.URL.Path,
			"elapsed", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHead(code int) {
	w.status = code
}

func (w statusWriter) Code() int {
	return cmp.Or(w.status, 200)
}
