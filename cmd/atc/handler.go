package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/davidmdm/x/xruntime"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/yoke"
)

func Handler(client *k8s.Client, cache *wasm.ModuleCache, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	commander := yoke.FromK8Client(client)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("route not found: %s", r.URL.Path), http.StatusNotFound)
	})

	// no op liveness and readiness checks
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {})

	mux.HandleFunc("POST /crdconvert/{airway}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		airway := r.PathValue("airway")

		converter := cache.Get(airway).Converter

		converter.RLock()
		defer converter.RUnlock()

		if converter.CompiledModule == nil {
			http.Error(w, "converter module not ready or validations not managed by this server", http.StatusNotFound)
			return
		}

		resp, err := wasi.Execute(ctx, wasi.ExecParams{
			CompiledModule: converter.CompiledModule,
			Stdin:          r.Body,
			Release:        "converter",
			CacheDir:       wasm.AirwayModuleDir(airway),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if _, err := w.Write(resp); err != nil {
			logger.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	mux.HandleFunc("POST /validations/{airway}", func(w http.ResponseWriter, r *http.Request) {
		var review admissionv1.AdmissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, fmt.Sprintf("failed to decode review: %v", err), http.StatusBadRequest)
			return
		}

		var cr unstructured.Unstructured
		if err := json.Unmarshal(review.Request.Object.Raw, &cr); err != nil {
			http.Error(w, fmt.Sprintf("failed to decode resource: %v", err), http.StatusBadRequest)
			return
		}

		airwayGVR := schema.GroupVersionResource{Group: "yoke.cd", Version: "v1alpha1", Resource: "airways"}

		rawAirway, err := client.Dynamic.Resource(airwayGVR).Get(r.Context(), r.PathValue("airway"), metav1.GetOptions{})
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get airway: %v", err), http.StatusInternalServerError)
			return
		}

		var airway v1alpha1.Airway
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(rawAirway.Object, &airway); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		object, _, err := unstructured.NestedFieldNoCopy(cr.Object, airway.Spec.ObjectPath...)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get object path: %v", err), http.StatusInternalServerError)
			return
		}

		data, err := json.Marshal(object)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to serialize flight input: %v", err), http.StatusInternalServerError)
			return
		}

		flight := cache.Get(airway.Name).Flight

		flight.RLock()
		defer flight.RUnlock()

		if flight.CompiledModule == nil {
			http.Error(w, "flight not ready or not registered for custom resource", http.StatusNotFound)
			return
		}

		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
			Result:  &metav1.Status{Status: metav1.StatusSuccess},
		}

		params := yoke.TakeoffParams{
			Release: atc.ReleaseName(&cr),
			Flight: yoke.FlightParams{
				WasmModule:          flight.CompiledModule,
				Input:               bytes.NewReader(data),
				Namespace:           cr.GetNamespace(),
				CompilationCacheDir: wasm.AirwayModuleDir(airway.Name),
			},
			CreateCRDs: airway.Spec.CreateCRDs,
			DryRun:     true,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: cr.GetAPIVersion(),
					Kind:       cr.GetKind(),
					Name:       cr.GetName(),
					UID:        cr.GetUID(),
				},
			},
		}

		if err := commander.Takeoff(r.Context(), params); err != nil && !internal.IsWarning(err) {
			review.Response.Allowed = false
			review.Response.Result = &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("applying resource returned errors during dry-run. Either the inputs are invalid or the package implementation has errors: %v", err),
				Reason:  metav1.StatusReasonInvalid,
			}
		}

		if err := json.NewEncoder(w).Encode(&review); err != nil {
			logger.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	mux.HandleFunc("POST /validations/airways.yoke.cd", func(w http.ResponseWriter, r *http.Request) {
		var review admissionv1.AdmissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var airway v1alpha1.Airway
		if err := json.Unmarshal(review.Request.Object.Raw, &airway); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		crd, err := internal.ToUnstructured(airway.CRD())
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to convert airway crd to unstructured object: %v", err), http.StatusBadRequest)
			return
		}

		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
		}
		review.Request = nil

		if err := client.ApplyResource(r.Context(), crd, k8s.ApplyOpts{DryRun: true}); err != nil {
			review.Response.Allowed = false
			review.Response.Result = &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("invalid crd template: %v", err),
				Reason:  metav1.StatusReasonInvalid,
			}
		}

		if err := json.NewEncoder(w).Encode(review); err != nil {
			logger.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	handler := withRecover(mux)
	handler = withLogger(logger, handler)

	return handler
}

func withLogger(logger *slog.Logger, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := statusWriter{ResponseWriter: w}

		handler.ServeHTTP(&sw, r)

		if sw.Code() == 200 && (r.URL.Path == "/live" || r.URL.Path == "/ready") {
			// Skip logging on simple liveness/readiness check passes as they polute the logs with information
			// that we don't need to see
			return
		}

		logger.Info(
			"request served",
			"code", sw.Code(),
			"method", r.Method,
			"path", r.URL.Path,
			"elapsed", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

func withRecover(handler http.Handler) http.Handler {
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
