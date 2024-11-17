package main

import (
	_ "embed"
	"log/slog"
	"net/http"
	"os"
)

//go:embed flight.wasm
var wasm []byte

var port = ":3000"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	logger.Info("booting up server", "port", port)

	http.ListenAndServe(port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(wasm); err != nil {
			logger.Error("failed to serve wasm asset", "err", err)
			return
		}
		logger.Info("successfully served wasm asset")
	}))
}
