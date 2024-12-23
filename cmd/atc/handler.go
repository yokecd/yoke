package main

import (
	"log/slog"
	"net/http"
	"os"

	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/wasi"
)

func Handler(locks *wasm.Locks, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// no op liveness and readiness checks
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {})

	mux.HandleFunc("POST /crdconvert/{airway}{$}", func(w http.ResponseWriter, r *http.Request) {
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
			log.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	return mux
}
