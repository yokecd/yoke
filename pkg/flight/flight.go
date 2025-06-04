package flight

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Release is a convenience function for fetching the release name within the context of an executing flight.
//
// For standalone Flights, this will be the name of the release passed to "yoke takeoff".
// For ATC Airflows, this will be the group.Kind.namespace.name of the Custom Resource.
// For YokeCD this will be the name of the corresponding Application.
//
// Note: When using Release in resource names or similar contexts, consider sanitizing the output first.
func Release() string {
	if _, release := filepath.Split(os.Getenv("YOKE_RELEASE")); release != "" {
		return release
	}
	_, release := filepath.Split(os.Args[0])
	return release
}

// Namespace is a convenience function for fetching the namespace within the context of an executing flight.
// This will generally be the -namespace flag passed to "yoke takeoff"
func Namespace() string {
	return cmp.Or(os.Getenv("YOKE_NAMESPACE"), os.Getenv("NAMESPACE"))
}

// YokeVersion returns the version of the yoke CLI or SDK used to run the flight.
func YokeVersion() string {
	return os.Getenv("YOKE_VERSION")
}

const (
	AnnotationOverrideFlight = "overrides.yoke.cd/flight"
	AnnotationOverrideMode   = "overrides.yoke.cd/mode"
)

// Resource is a best effort attempt at capturing the set of types that are kubernetes resources.
// K8s resource embed the metav1.TypeMeta struct and thus expose this method; unstructured.Unstructured objects also expose it.
//
// Having this type allows us to not fallback to using `any` when building our flight implementations.
type Resource interface {
	GroupVersionKind() schema.GroupVersionKind
}

// Resources represents a single deployment stage. A stage is a valid flight output.
// Nil resource elements are ignored when marshalling.
type Resources []Resource

// MarshalJSON implements custom JSON marshalling for flight stages. It allows stages to have resources written as nil instead of omitting them entirely.
// To support this convenience, a stage filters out nil values before serializing its content.
func (resources Resources) MarshalJSON() ([]byte, error) {
	filtered := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		if reflect.ValueOf(resource).IsNil() {
			continue
		}
		filtered = append(filtered, resource)
	}
	return json.Marshal(filtered)
}

// Stages is an ordered list of stages. Yoke will apply each stage one by one.
// Stages is a valid flight output.
type Stages []Resources
