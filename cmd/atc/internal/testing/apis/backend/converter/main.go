package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"

	v1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	v2 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var review apiextensionsv1.ConversionReview

	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&review); err != nil {
		return fmt.Errorf("failed to parse ConversionRequest: %v", err)
	}

	resp := Convert(review.Request)
	resp.UID = review.Request.UID

	review.Request = nil
	review.Response = resp

	return json.NewEncoder(os.Stdout).Encode(review)
}

func Convert(req *apiextensionsv1.ConversionRequest) *apiextensionsv1.ConversionResponse {
	gv, err := schema.ParseGroupVersion(req.DesiredAPIVersion)
	if err != nil {
		return &apiextensionsv1.ConversionResponse{
			Result: metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("could not parse desired api version: %v", err),
				Reason:  metav1.StatusReasonBadRequest,
			},
		}
	}

	convert := func() ConverterFunc {
		if gv.Version == "v1" {
			return MakeConverter(V2ToV1)
		}
		return MakeConverter(V1ToV2)
	}()

	converted := make([]runtime.RawExtension, len(req.Objects))
	for i, obj := range req.Objects {
		extension, status := convert(obj.Raw)
		if status != nil {
			return &apiextensionsv1.ConversionResponse{Result: *status}
		}
		converted[i] = *extension
	}

	return &apiextensionsv1.ConversionResponse{
		Result:           metav1.Status{Status: metav1.StatusSuccess},
		ConvertedObjects: converted,
	}
}

type ConverterFunc func(raw []byte) (result *runtime.RawExtension, status *metav1.Status)

func MakeConverter[From any, To any](fn func(From) To) ConverterFunc {
	return func(raw []byte) (result *runtime.RawExtension, status *metav1.Status) {
		var source From

		if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(raw)).Decode(&source); err != nil {
			return nil, &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("failed to parse source object as v1 backend: %v", err),
				Reason:  metav1.StatusReasonInternalError,
			}
		}

		extension, err := toRawExtension(fn(source))
		if err != nil {
			return nil, &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("failed to convert to target: %v", err),
				Reason:  metav1.StatusReasonInternalError,
			}
		}

		return &extension, nil
	}
}

func toRawExtension[T any](value T) (runtime.RawExtension, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return runtime.RawExtension{}, err
	}

	var resource unstructured.Unstructured
	if err := json.Unmarshal(raw, &resource); err != nil {
		return runtime.RawExtension{}, err
	}

	return runtime.RawExtension{
		Raw:    raw,
		Object: &resource,
	}, nil
}

const AnnotationKeyPrefix = "annotations.examples.com/"

func V1ToV2(source v1.Backend) v2.Backend {
	annotations := make(map[string]string)
	for key, value := range source.Annotations {
		annotation, ok := strings.CutPrefix(key, AnnotationKeyPrefix)
		if !ok {
			continue
		}
		annotations[annotation] = value
	}

	return v2.Backend{
		ObjectMeta: metav1.ObjectMeta{
			Name:        source.Name,
			Namespace:   source.Namespace,
			UID:         source.UID,
			Labels:      source.Labels,
			Annotations: source.Annotations,
		},
		Spec: v2.BackendSpec{
			Img:      source.Spec.Image,
			Replicas: source.Spec.Replicas,
			Meta: v2.Meta{
				Labels:      source.Spec.Labels,
				Annotations: annotations,
			},
			NodePort:    source.Spec.NodePort,
			ServicePort: source.Spec.ServicePort,
		},
	}
}

func V2ToV1(source v2.Backend) v1.Backend {
	annotations := maps.Clone(source.Annotations)
	for key, value := range source.Spec.Meta.Annotations {
		annotations[path.Join(AnnotationKeyPrefix, key)] = value
	}

	return v1.Backend{
		ObjectMeta: metav1.ObjectMeta{
			Name:        source.Name,
			Namespace:   source.Namespace,
			UID:         source.UID,
			Labels:      source.Labels,
			Annotations: annotations,
		},
		Spec: v1.BackendSpec{
			Image:       source.Spec.Img,
			Replicas:    source.Spec.Replicas,
			Labels:      source.Spec.Meta.Labels,
			NodePort:    source.Spec.NodePort,
			ServicePort: source.Spec.ServicePort,
		},
	}
}
