package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/davidmdm/x/xruntime"

	admissionv1 "k8s.io/api/admission/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/yoke"
)

func Handler(client *k8s.Client, cache *wasm.ModuleCache, controllers *atc.ControllerCache, logger *slog.Logger) http.Handler {
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

		if converter.Instance.CompiledModule == nil {
			http.Error(w, "converter module not ready or validations not managed by this server", http.StatusNotFound)
			return
		}

		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var desiredAPIVersion string

		var review apiextensionsv1.ConversionReview
		if err := json.Unmarshal(data, &review); err == nil {
			desiredAPIVersion = review.Request.DesiredAPIVersion
		}

		originals := make([]map[string]any, len(review.Request.Objects))
		for i, obj := range review.Request.Objects {
			if err := json.Unmarshal(obj.Raw, &originals[i]); err != nil {
				http.Error(w, fmt.Sprintf("could not Unmarshal request object: %v", err), http.StatusBadRequest)
				return
			}
		}

		resp, err := wasi.Execute(ctx, wasi.ExecParams{
			Module:  converter.Instance,
			Stdin:   bytes.NewReader(data),
			Release: "converter",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.Unmarshal(resp, &review); err != nil {
			http.Error(w, fmt.Sprintf("failed to parse review response: %v", err), http.StatusInternalServerError)
			return
		}

		for i, converted := range review.Response.ConvertedObjects {
			originalStatus, _, err := unstructured.NestedMap(originals[i], "status")
			if err != nil {
				continue
			}

			var raw map[string]any
			if err := json.Unmarshal(converted.Raw, &raw); err != nil {
				continue
			}

			unstructured.SetNestedField(raw, originalStatus, "status")

			data, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			converted.Raw = data
		}

		addRequestAttrs(r.Context(), slog.Group(
			"converter",
			"status", review.Response.Result.Status,
			"reason", review.Response.Result.Reason,
			"count", len(review.Response.ConvertedObjects),
			"uid", review.Response.UID,
			"DesiredAPIVersion", desiredAPIVersion,
		))

		if err := json.NewEncoder(w).Encode(review); err != nil {
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

		if airway.Spec.SkipAdmissionWebhook {
			review.Response = &admissionv1.AdmissionResponse{
				UID:     review.Request.UID,
				Allowed: true,
				Result:  &metav1.Status{Status: metav1.StatusSuccess, Message: "admission skipped"},
			}
			review.Request = nil

			addRequestAttrs(r.Context(), slog.Bool("skipped", true))

			json.NewEncoder(w).Encode(&review)
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

		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
			Result:  &metav1.Status{Status: metav1.StatusSuccess},
		}

		params := yoke.TakeoffParams{
			Release:               atc.ReleaseName(&cr),
			CrossNamespace:        airway.Spec.CrossNamespace,
			ClusterAccess:         airway.Spec.ClusterAccess,
			ClusterResourceAccess: airway.Spec.ResourceAccessMatchers,
			Flight: yoke.FlightParams{
				Input:     bytes.NewReader(data),
				Namespace: cr.GetNamespace(),
			},
			DryRun:         true,
			ForceOwnership: true,
			ForceConflicts: true,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: cr.GetAPIVersion(),
					Kind:       cr.GetKind(),
					Name:       cr.GetName(),
					UID:        cr.GetUID(),
				},
			},
			IdentityFunc: func(item *unstructured.Unstructured) bool {
				return item.GroupVersionKind().GroupKind() == schema.GroupKind{
					Group: airway.Spec.Template.Group,
					Kind:  airway.Spec.Template.Names.Kind,
				} &&
					item.GetName() == cr.GetName() &&
					item.GetNamespace() == cr.GetNamespace()
			},
		}

		if overrideURL, _, _ := unstructured.NestedString(cr.Object, "metadata", "annotations", flight.AnnotationOverrideFlight); overrideURL != "" {
			addRequestAttrs(r.Context(), slog.Group("overrides", "flight", overrideURL))
			params.Flight.Path = overrideURL
		} else {
			flightMod := cache.Get(airway.Name).Flight

			flightMod.RLock()
			defer flightMod.RUnlock()

			if flightMod.Instance.CompiledModule == nil {
				http.Error(w, "flight not ready or not registered for custom resource", http.StatusNotFound)
				return
			}
			params.Flight.Module = flightMod.Module
		}

		ctx := internal.WithStderr(r.Context(), io.Discard)

		if err := commander.Takeoff(ctx, params); err != nil && !internal.IsWarning(err) {
			review.Response.Allowed = false
			review.Response.Result = &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("applying resource returned errors during dry-run. Either the inputs are invalid or the package implementation has errors: %v", err),
				Reason:  metav1.StatusReasonInvalid,
			}
		}

		addRequestAttrs(r.Context(), slog.Group("validation", "allowed", review.Response.Allowed, "status", review.Response.Result.Reason))

		if err := json.NewEncoder(w).Encode(&review); err != nil {
			logger.Error("unexpected: failed to write response to connection", "error", err)
		}
	})

	mux.HandleFunc("POST /validations/resources", func(w http.ResponseWriter, r *http.Request) {
		var review admissionv1.AdmissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		addRequestAttrs(r.Context(), slog.String("user", review.Request.UserInfo.Username))

		prev, err := UnstructuredFromRawExt(review.Request.OldObject)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		next, err := UnstructuredFromRawExt(review.Request.Object)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
			Result: &metav1.Status{
				Status:  metav1.StatusSuccess,
				Message: "validation passed",
			},
		}
		defer func() {
			review.Request = nil
			addRequestAttrs(r.Context(), slog.Group(
				"validation",
				"allowed", review.Response.Allowed,
				"details", review.Response.Result.Message,
			))
			json.NewEncoder(w).Encode(review)
		}()

		addRequestAttrs(r.Context(), slog.String("resourceId", internal.Canonical(prev)))

		if next != nil && internal.GetOwner(prev) != internal.GetOwner(next) {
			review.Response.Allowed = false
			review.Response.Result = &metav1.Status{
				Message: "cannot modify yoke labels",
				Status:  metav1.StatusFailure,
				Reason:  metav1.StatusReasonBadRequest,
			}
			return
		}

		if next != nil && internal.ResourcesAreEqualWithStatus(prev, next) {
			addRequestAttrs(r.Context(), slog.String("skipReason", "resources are equal"))
			return
		}

		owners := prev.GetOwnerReferences()
		if len(owners) == 0 {
			addRequestAttrs(r.Context(), slog.String("skipReason", "no owner references"))
			return
		}

		owner := owners[0]

		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			addRequestAttrs(
				r.Context(),
				slog.String("skipReason", "failed to parse owner apiVersion"),
				slog.String("error", err.Error()),
			)
			return
		}

		ownerGroupKind := schema.GroupKind{Group: gv.Group, Kind: owner.Kind}.String()
		controller, ok := controllers.Load(ownerGroupKind)

		addRequestAttrs(
			r.Context(),
			slog.String("ownerGroupKind", ownerGroupKind),
			slog.Bool("matchedController", ok),
		)

		if !ok {
			addRequestAttrs(r.Context(), slog.String("skipReason", "no registered flight controller"))
			return
		}

		labels := prev.GetLabels()
		release := labels[internal.LabelYokeRelease]
		namespace := labels[internal.LabelYokeReleaseNS]

		flightState, ok := controller.FlightState(prev.GetName(), namespace)
		if !ok {
			addRequestAttrs(r.Context(), slog.String("skipReason", "no flight state"), slog.String("ERROR", "unexpected: no flight state associated to resource"))
		}

		if flightState.ClusterAccess {
			// We do not want to be sending resource update events to the controller if it is currently mutating state
			// as we want to avoid race conditions on admission.
			// Example:
			// - Controller is reading cluster state and reads a secret Value
			// - Secret is updated after having passed admission
			// - Controller uses stale secret data and applies it.
			//
			// With this mutex, if the controller is reading and applying state, we wait, then grab a read lock.
			//
			// This makes sure the controller finishes its read/write operations before its subresources can get updated.
			flightState.Mutex.RLock()
			defer flightState.Mutex.RUnlock()

			addRequestAttrs(r.Context(), slog.Bool("admissionLocked", true))
		}

		addRequestAttrs(r.Context(), slog.String("airwayMode", string(flightState.Mode)))

		switch flightState.Mode {
		case v1alpha1.AirwayModeStatic:
			if next == nil || !next.GetDeletionTimestamp().IsZero() {
				review.Response.Allowed = false
				review.Response.Result = &metav1.Status{
					Message: "cannot delete resources managed by Air-Traffic-Controller",
					Status:  metav1.StatusFailure,
					Reason:  metav1.StatusReasonBadRequest,
				}
				return
			}

			release, err := client.GetRelease(r.Context(), release, namespace)
			if err != nil {
				addRequestAttrs(r.Context(), slog.String("skipReason", fmt.Sprintf("failed to get release: %v", err)))
				return
			}
			if len(release.History) == 0 {
				addRequestAttrs(r.Context(), slog.String("skipReason", "no release history found"))
				return
			}

			stages, err := client.GetRevisionResources(r.Context(), release.ActiveRevision())
			if err != nil {
				addRequestAttrs(r.Context(), slog.String("skipReason", fmt.Sprintf("failed to get release resources: %v", err)))
				return
			}

			resources := stages.Flatten()

			desired, ok := internal.Find(resources, func(resource *unstructured.Unstructured) bool {
				return resource.GetKind() == next.GetKind() &&
					resource.GetAPIVersion() == next.GetAPIVersion() &&
					resource.GetName() == next.GetName()
			})
			if !ok {
				addRequestAttrs(r.Context(), slog.String("skipReason", "could not find desired resource in release"))
				return
			}

			internal.RemoveAdditions(desired, next)

			if !internal.ResourcesAreEqual(desired, next) {
				review.Response.Allowed = false
				review.Response.Result = &metav1.Status{
					Message: "cannot modify flight sub-resources",
					Status:  metav1.StatusFailure,
					Reason:  metav1.StatusReasonBadRequest,
				}
			}

		case v1alpha1.AirwayModeDynamic:
			evt := ctrl.Event{
				Name: owner.Name,
				// TODO: will this always be the same namespace as the flight?
				// Ownership will always be in the same namespace...
				// Perhaps support for cluster scope owners needs to be thought of?
				// Also crossNamespace deployments?
				Namespace: namespace,
			}

			addRequestAttrs(r.Context(), slog.String("generatedEvent", evt.String()))

			controller.SendEvent(evt)
		default:
			return
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

type keyReqAttrs struct{}

func withRequestAttrs(ctx context.Context, attrs *[]slog.Attr) context.Context {
	return context.WithValue(ctx, keyReqAttrs{}, attrs)
}

func addRequestAttrs(ctx context.Context, attrs ...slog.Attr) {
	reqAttrs, _ := ctx.Value(keyReqAttrs{}).(*[]slog.Attr)
	if reqAttrs == nil {
		return
	}
	*reqAttrs = append(*reqAttrs, attrs...)
}

func UnstructuredFromRawExt(ext runtime.RawExtension) (*unstructured.Unstructured, error) {
	if len(ext.Raw) == 0 {
		return nil, nil
	}

	var resource unstructured.Unstructured
	if err := json.Unmarshal(ext.Raw, &resource); err != nil {
		return nil, err
	}

	return &resource, nil
}
