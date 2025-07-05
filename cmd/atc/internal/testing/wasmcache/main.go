package main

import (
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

//go:embed all:wasm
var wasm embed.FS

var port = ":3000"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	logger.Info("booting up server", "port", port)

	if err := http.ListenAndServe(port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "wasm/" + strings.TrimLeft(r.URL.Path, "/")

		data, err := wasm.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, fmt.Sprintf("%s: not found", path), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		logger := logger.With("file", path)

		if _, err := w.Write(data); err != nil {
			logger.Error("failed to serve wasm asset", "err", err)
			return
		}
		logger.Info("successfully served wasm asset")
	})); err != nil {
		logger.Error("error in listening", "error", err.Error())
	}
}
