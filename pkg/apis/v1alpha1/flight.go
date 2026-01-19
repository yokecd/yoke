package v1alpha1

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/apis"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

const (
	KindFlight        = "Flight"
	KindClusterFlight = "ClusterFlight"
)

type Flight struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              FlightSpec    `json:"spec"`
	Status            flight.Status `json:"status,omitzero"`
}

func (Flight) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	type alt Flight
	schema := openapi.SchemaFrom(reflect.TypeFor[alt]())
	schema.Description = strings.Join(
		[]string{
			"Flights allow you to create yoke releases in your cluster using the air traffic controller instead of a client-side implementation",
			"like with the core yoke cli.",
			"The Flight custom resource let's you specify the flight module via a URL (https or oci) and invoke it with some arbitrary stdin and arguments.",
			"Flight resources are namespaced, and the underlying resources it produces must be part of the same namespace.",
			"For cluster scoped flights see the ClusterFlight custom resource.",
		},
		" ",
	)
	return schema
}

func FlightGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "yoke.cd",
		Version:  "v1alpha1",
		Resource: "flights",
	}
}

func ClusterFlightGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "yoke.cd",
		Version:  "v1alpha1",
		Resource: "clusterflights",
	}
}

type FlightSpec struct {
	// WasmURLs defines the locations for the various implementations the AirTrafficController will invoke.
	WasmURL string `json:"wasmUrl" Description:"URL of flight module. Can be http(s) or oci."`

	// Input will be passed to the flight over stdin. Flights are wasm programs and a string representation of the input
	// is the most practical. This means that the input is not constrained to being json/yaml. It can be binary (base64), cue, toml,
	// or any input expected by the underlying flight implementation.
	// As a convenience, you can use InputObject for json/yaml formats. However Input takes precedence InputObject.
	Input string `json:"input,omitzero" Description:"Raw input for for flight STDIN."`

	// InputObject will be marshalled to JSON and passed as the input to the flight.
	// This is a convenience to allow users to write their inputs within the json/yaml context of the flight resource
	// instead of under a string property like Input. This field has no effect if Input is defined.
	InputObject map[string]any `json:"inputObject,omitzero" Description:"Convenience for writing json/yaml objects directly in CR. Has no effect if input is specified."`

	Args []string `json:"args,omitempty" Description:"List of command-line args to be passed to flight during execution."`

	// FixDriftInterval sets an interval at which the resource is requeued for evaluation by the AirTrafficController.
	// The ATC will attempt to reapply the resource. In most cases this will result in a noop. If however a user
	// changed any of the underlying resource's configuration, this will be set back via this mechanism. It allows
	// you to enforce the desired state of your resource against external manipulation.
	FixDriftInterval metav1.Duration `json:"fixDriftInterval,omitzero" Description:"Interval to requeue flight for evaluation. Self-healing mechanism."`

	// ClusterAccess allows the flight to lookup resources in the cluster. Resources are limited to those owned by the calling release.
	ClusterAccess bool `json:"clusterAccess,omitempty" Default:"false" Description:"Allow flight access to the cluster via WASI SDK."`

	// ResourceAccessMatchers combined with ClusterAccess allow you to lookup any resource in your cluster. By default without any matchers
	// the only resources that you can lookup are resources that are directly owned by the release. If you wish to access resources external
	// to the release you can provide a set of matcher patterns. If any pattern matches, the resource is allowed to by accessed.
	//
	// The pattern goes like this: $namespace/$Kind.Group:$name
	// Where namespace and name are optional. If they are omitted it is the same as setting them to '*'.
	//
	// Examples Matchers:
	// 	- Deployment.apps 							# matches all deployments in your cluster
	// 	- foo/Deployment.apps 					# matches all deployments in namespace foo
	// 	- foo/Deployment.apps:example 	# matches a deployment named example in namespace foo.
	// 	- * 														# matches all resources in the cluster.
	// 	- foo/* 												# matches all resources in namespace foo.
	ResourceAccessMatchers []string `json:"resourceAccessMatchers,omitempty" Description:"ResourceMatcher expressions to allow explicit access to resources not owned by the flight."`

	// Insecure only applies to flights using OCI urls. Allows image references to be fetched without TLS verification.
	Insecure bool `json:"insecure,omitempty" Description:"Insecure only applies to flights using OCI urls. Allows image references to be fetched without TLS verification."`

	// SkipAdmissionWebhook bypasses admission webhook for the airway's CRs.
	// The admission webhook validates that the resources that would be created pass a dry-run phase.
	// However in the case of some multi-stage implementations, stages that depend on prior stages cannot pass dry-run.
	// In this case there is no option but to skip the admission webhook.
	//
	// Therefore multi-stage Airways are not generally recommended.
	SkipAdmissionWebhook bool `json:"skipAdmissionWebhook,omitempty" Description:"Skip admission validation for your flight."`

	// HistoryCapSize controls how many revisions of an instance (custom resource) of this airway is kept in history.
	// To make it uncapped set this value to any negative integer.
	// By default 2.
	HistoryCapSize int `json:"historyCapSize,omitempty" Description:"Max length of history for releases generated by your flight. Default is 2"`

	// Prune enables pruning for resources that are not automatically pruned between updates or on deletion.
	Prune PruneOptions `json:"prune,omitzero" Description:"Options for pruning sensitive resources on deletion."`

	// MaxMemoryMib sets the maximum amount of memory an Airway instance's flight execution can allocate.
	// Leaving it unset will allow the maximum amount of memory which is 4Gib. It is recommended to set a reasonable maximum
	// when working with third party flights.
	MaxMemoryMib uint32 `json:"maxMemoryMib,omitzero" Description:"Maximum amounts of Mib to allow the flight to allocate. Default is 4Gib."`

	// Timeout is the timeout for the airway instance's flight execution. Default setting is 10s.
	Timeout metav1.Duration `json:"timeout,omitzero" Description:"Maximum execution duration before flight is cancelled."`
}

func (FlightSpec) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	type alt FlightSpec
	schema := openapi.SchemaFrom(reflect.TypeFor[alt]())
	schema.Description = "Specification of the Flight and ClusterFlight custom resources."
	return schema
}

func (flight Flight) MarshalJSON() ([]byte, error) {
	flight.Kind = KindFlight
	flight.APIVersion = APIVersion
	type alt Flight
	return json.Marshal(alt(flight))
}

var _ runtime.Object = (*Flight)(nil)

func (flight *Flight) DeepCopyObject() runtime.Object {
	return apis.DeepCopy(flight)
}

type ClusterFlight Flight

func (ClusterFlight) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	type alt ClusterFlight
	schema := openapi.SchemaFrom(reflect.TypeFor[alt]())
	schema.Description = "Cluser scoped version of the yoke.cd Flight resource. For more information see Flight."
	return schema
}

func (flight ClusterFlight) MarshalJSON() ([]byte, error) {
	flight.Kind = KindClusterFlight
	flight.APIVersion = APIVersion
	type alt ClusterFlight
	return json.Marshal(alt(flight))
}

func FlightInputStream(spec FlightSpec) io.Reader {
	if spec.Input != "" {
		return strings.NewReader(spec.Input)
	}
	if spec.InputObject != nil {
		return internal.JSONReader(spec.InputObject)
	}
	return strings.NewReader("")
}

var _ runtime.Object = (*ClusterFlight)(nil)

func (flight *ClusterFlight) DeepCopyObject() runtime.Object {
	return apis.DeepCopy(flight)
}
