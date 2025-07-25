package xhttp

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/davidmdm/x/xruntime"
)

func WithLogger(logger *slog.Logger, handler http.Handler) http.Handler {
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

		base := []slog.Attr{
			slog.Int("code", sw.Code()),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("elapsed", time.Since(start).Round(time.Millisecond).String()),
		}

		logger.LogAttrs(r.Context(), slog.LevelInfo, "request served", append(base, attrs...)...)
	})
}

func WithRecover(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				http.Error(
					w,
					fmt.Sprintf("recovered from panic: %v: %s", e, xruntime.CallStack(-1)),
					http.StatusInternalServerError,
				)
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
