package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/atc/wasm"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/k8s/ctrl"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasi/host"
	"github.com/yokecd/yoke/internal/xhttp"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/yoke"
)

func Handler(client *k8s.Client, cache *wasm.ModuleCache, controllers *atc.ControllerCache, dispatcher *atc.EventDispatcher, logger *slog.Logger, filter xhttp.LogFilterFunc) http.Handler {
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

		xhttp.AddRequestAttrs(r.Context(), slog.Group(
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

		shouldCheckAirwayPerms, err := func() (bool, error) {
			switch review.Request.Operation {
			case admissionv1.Create:
				return internal.GetAnnotation(cr, flight.AnnotationOverrideFlight) != "" || internal.GetAnnotation(cr, flight.AnnotationOverrideMode) != "", nil

			case admissionv1.Update:
				var oldCr unstructured.Unstructured
				if err := json.Unmarshal(review.Request.OldObject.Raw, &oldCr); err != nil {
					return false, fmt.Errorf("failed to decode old resource: %w", err)
				}
				return false ||
					internal.GetAnnotation(oldCr, flight.AnnotationOverrideFlight) != internal.GetAnnotation(cr, flight.AnnotationOverrideFlight) ||
					internal.GetAnnotation(oldCr, flight.AnnotationOverrideMode) != internal.GetAnnotation(cr, flight.AnnotationOverrideMode), nil

			default:
				return false, nil
			}
		}()
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}

		if shouldCheckAirwayPerms {
			accessReview, err := client.Clientset.AuthorizationV1().SubjectAccessReviews().Create(
				r.Context(),
				&authorizationv1.SubjectAccessReview{
					TypeMeta: metav1.TypeMeta{
						Kind:       "SubectAccessReview",
						APIVersion: authorizationv1.SchemeGroupVersion.Identifier(),
					},
					Spec: authorizationv1.SubjectAccessReviewSpec{
						UID:    review.Request.UserInfo.UID,
						User:   review.Request.UserInfo.Username,
						Groups: review.Request.UserInfo.Groups,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Verb:     "update",
							Group:    "yoke.cd",
							Version:  "v1alpha1",
							Resource: "airways",
						},
					},
				},
				metav1.CreateOptions{},
			)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to perform access review: %v", err), http.StatusInternalServerError)
				return
			}

			if !accessReview.Status.Allowed {
				review.Response = &admissionv1.AdmissionResponse{
					UID:     review.Request.UID,
					Allowed: false,
					Result: &metav1.Status{
						Status:  metav1.StatusFailure,
						Reason:  metav1.StatusReasonForbidden,
						Message: "user does not have permissions to create or update override annotations",
					},
				}
				review.Request = nil
				json.NewEncoder(w).Encode(&review)
				return
			}
		}

		airway, err := client.AirwayIntf.Get(r.Context(), r.PathValue("airway"), metav1.GetOptions{})
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get airway: %v", err), http.StatusInternalServerError)
			return
		}

		if airway.Spec.SkipAdmissionWebhook {
			review.Response = &admissionv1.AdmissionResponse{
				UID:     review.Request.UID,
				Allowed: true,
				Result:  &metav1.Status{Status: metav1.StatusSuccess, Message: "admission skipped"},
			}
			review.Request = nil

			xhttp.AddRequestAttrs(r.Context(), slog.Bool("skipped", true))

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
			Release:        atc.ReleaseName(&cr),
			Namespace:      cmp.Or(cr.GetNamespace(), "default"),
			CrossNamespace: airway.Spec.CrossNamespace,
			ClusterAccess: host.ClusterAccessParams{
				Enabled:          airway.Spec.ClusterAccess,
				ResourceMatchers: airway.Spec.ResourceAccessMatchers,
			},
			Flight: yoke.FlightParams{
				Input:        bytes.NewReader(data),
				MaxMemoryMib: uint64(airway.Spec.MaxMemoryMib),
				Timeout:      airway.Spec.Timeout.Duration,
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
			xhttp.AddRequestAttrs(r.Context(), slog.Group("overrides", "flight", overrideURL))
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

		xhttp.AddRequestAttrs(r.Context(), slog.Group("validation", "allowed", review.Response.Allowed, "status", review.Response.Result.Reason))

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

		xhttp.AddRequestAttrs(r.Context(), slog.String("user", review.Request.UserInfo.Username))
		xhttp.AddRequestAttrs(r.Context(), slog.String("operation", string(review.Request.Operation)))

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
			xhttp.AddRequestAttrs(r.Context(), slog.Group(
				"validation",
				"allowed", review.Response.Allowed,
				"details", review.Response.Result.Message,
			))
			json.NewEncoder(w).Encode(review)
		}()

		xhttp.AddRequestAttrs(r.Context(), slog.String("resourceId", internal.ResourceRef(prev)))

		if next != nil {
			atcLabels := []string{
				internal.LabelYokeRelease,
				internal.LabelYokeReleaseNS,
				atc.LabelInstanceName,
				atc.LabelInstanceNamespace,
				atc.LabelInstanceGroupKind,
			}

			var errs []error
			for _, label := range atcLabels {
				if internal.GetLabel(prev, label) != internal.GetLabel(next, label) {
					errs = append(errs, fmt.Errorf("%s", label))
				}
			}
			if err := xerr.MultiErrFrom("cannot modify yoke labels", errs...); err != nil {
				review.Response.Allowed = false
				review.Response.Result = &metav1.Status{
					Message: err.Error(),
					Status:  metav1.StatusFailure,
					Reason:  metav1.StatusReasonBadRequest,
				}
				return
			}
		}

		if next != nil && internal.ResourcesAreEqualWithStatus(prev, next) {
			xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", "resources are equal"))
			return
		}

		var (
			instanceName      = internal.GetLabel(prev, atc.LabelInstanceName)
			instanceNamespace = internal.GetLabel(prev, atc.LabelInstanceNamespace)
			instanceGK        = internal.GetLabel(prev, atc.LabelInstanceGroupKind)
		)

		controller, ok := controllers.Load(instanceGK)

		xhttp.AddRequestAttrs(
			r.Context(),
			slog.String("instanceGroupKind", instanceGK),
			slog.Bool("matchedController", ok),
		)

		if !ok {
			xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", "no registered flight controller"))
			return
		}

		flightState, ok := controller.FlightState(instanceName, instanceNamespace)
		if !ok {
			xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", "no flight state"), slog.String("ERROR", "unexpected: no flight state associated to resource"))
			return
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

			xhttp.AddRequestAttrs(r.Context(), slog.Bool("admissionLocked", true))
		}

		xhttp.AddRequestAttrs(r.Context(), slog.String("airwayMode", string(flightState.Mode)))

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

			release, err := client.GetRelease(
				r.Context(),
				internal.GetLabel(prev, internal.LabelYokeRelease),
				internal.GetLabel(prev, internal.LabelYokeReleaseNS),
			)
			if err != nil {
				xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", fmt.Sprintf("failed to get release: %v", err)))
				return
			}
			if len(release.History) == 0 {
				xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", "no release history found"))
				return
			}

			stages, err := client.GetRevisionResources(r.Context(), release.ActiveRevision())
			if err != nil {
				xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", fmt.Sprintf("failed to get release resources: %v", err)))
				return
			}

			resources := stages.Flatten()

			desired, ok := internal.Find(resources, func(resource *unstructured.Unstructured) bool {
				return resource.GetKind() == next.GetKind() &&
					resource.GetAPIVersion() == next.GetAPIVersion() &&
					resource.GetName() == next.GetName()
			})
			if !ok {
				xhttp.AddRequestAttrs(r.Context(), slog.String("skipReason", "could not find desired resource in release"))
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
				Name:      instanceName,
				Namespace: instanceNamespace,
			}

			xhttp.AddRequestAttrs(r.Context(), slog.String("generatedEvent", evt.String()))

			controller.SendEvent(evt)
		default:
			return
		}
	})

	mux.HandleFunc("POST /validations/external-resources", func(w http.ResponseWriter, r *http.Request) {
		var review admissionv1.AdmissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

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

		resource := func() string {
			if next != nil {
				return internal.ResourceRef(next)
			}
			return internal.ResourceRef(prev)
		}()

		dispatches := dispatcher.Dispatch(resource)

		for _, value := range dispatches {
			logger.Info(
				"external resource dispatch event",
				"triggeringResource", resource,
				"controller", value.GK, "eventName",
				value.Name, "eventNamesace", value.Namespace,
			)
		}

		xhttp.AddRequestAttrs(r.Context(), slog.String("user", review.Request.UserInfo.Username))
		xhttp.AddRequestAttrs(r.Context(), slog.String("operation", string(review.Request.Operation)))
		xhttp.AddRequestAttrs(r.Context(), slog.String("resource", resource))
		xhttp.AddRequestAttrs(r.Context(), slog.Int("dispatchCount", len(dispatches)))

		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: true,
			Result: &metav1.Status{
				Status:  metav1.StatusSuccess,
				Message: "validation passed",
			},
		}
		review.Request = nil

		if err := json.NewEncoder(w).Encode(review); err != nil {
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

	handler := xhttp.WithRecover(mux)
	handler = xhttp.WithLogger(logger, handler, filter)

	return handler
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
