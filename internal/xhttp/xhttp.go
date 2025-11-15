package xhttp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/docker/go-units"

	"github.com/davidmdm/x/xruntime"
)

type LogFilterFunc func(pattern string, attrs []slog.Attr) bool

func WithLogger(logger *slog.Logger, handler http.Handler, filter LogFilterFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := statusWriter{ResponseWriter: w}

		var attrs []slog.Attr
		r = r.WithContext(withRequestAttrs(r.Context(), &attrs))

		handler.ServeHTTP(&sw, r)

		if sw.Code() == 200 && (r.URL.Path == "/live" || r.URL.Path == "/ready") {
			// Skip logging on simple liveness/readiness check passes as they polute the logs with information
			// that we don't need to see
			return
		}

		base := append(
			[]slog.Attr{
				slog.Int("code", sw.Code()),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("elapsed", time.Since(start).Round(time.Millisecond).String()),
			},
			attrs...,
		)

		if filter == nil || filter(r.Pattern, base) {
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request served", base...)
		}
	})
}

func WithRecover(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				fmt.Println("[recovered from panic]", xruntime.CallStack(-1))
				http.Error(w, "panic", http.StatusInternalServerError)
				return
			}
		}()
		handler.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w statusWriter) Code() int {
	return cmp.Or(w.status, 200)
}

type keyReqAttrs struct{}

func withRequestAttrs(ctx context.Context, attrs *[]slog.Attr) context.Context {
	return context.WithValue(ctx, keyReqAttrs{}, attrs)
}

func AddRequestAttrs(ctx context.Context, attrs ...slog.Attr) {
	reqAttrs, _ := ctx.Value(keyReqAttrs{}).(*[]slog.Attr)
	if reqAttrs == nil {
		return
	}
	*reqAttrs = append(*reqAttrs, attrs...)
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

func MemStatHandler(w http.ResponseWriter, r *http.Request) {
	runGC, _ := strconv.ParseBool(r.URL.Query().Get("runGC"))
	if runGC {
		runtime.GC()
	}

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
}
