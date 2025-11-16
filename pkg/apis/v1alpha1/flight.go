package v1alpha1

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/pkg/flight"
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
	WasmURL string `json:"wasmUrl"`

	Input string `json:"input,omitzero"`

	Args []string `json:"args,omitempty"`

	// FixDriftInterval sets an interval at which the resource is requeued for evaluation by the AirTrafficController.
	// The ATC will attempt to reapply the resource. In most cases this will result in a noop. If however a user
	// changed any of the underlying resource's configuration, this will be set back via this mechanism. It allows
	// you to enforce the desired state of your resource against external manipulation.
	FixDriftInterval metav1.Duration `json:"fixDriftInterval,omitzero"`

	// ClusterAccess allows the flight to lookup resources in the cluster. Resources are limited to those owned by the calling release.
	ClusterAccess bool `json:"clusterAccess,omitempty" Default:"false"`

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
	ResourceAccessMatchers []string `json:"resourceAccessMatchers,omitempty"`

	// Insecure only applies to flights using OCI urls. Allows image references to be fetched without TLS verification.
	Insecure bool `json:"insecure,omitempty"`

	// SkipAdmissionWebhook bypasses admission webhook for the airway's CRs.
	// The admission webhook validates that the resources that would be created pass a dry-run phase.
	// However in the case of some multi-stage implementations, stages that depend on prior stages cannot pass dry-run.
	// In this case there is no option but to skip the admission webhook.
	//
	// Therefore multi-stage Airways are not generally recommended.
	SkipAdmissionWebhook bool `json:"skipAdmissionWebhook,omitempty"`

	// HistoryCapSize controls how many revisions of an instance (custom resource) of this airway is kept in history.
	// To make it uncapped set this value to any negative integer.
	// By default 2.
	HistoryCapSize int `json:"historyCapSize,omitempty"`

	// Prune enables pruning for resources that are not automatically pruned between updates or on deletion.
	Prune PruneOptions `json:"prune,omitzero"`

	// MaxMemoryMib sets the maximum amount of memory an Airway instance's flight execution can allocate.
	// Leaving it unset will allow the maximum amount of memory which is 4Gib. It is recommended to set a reasonable maximum
	// when working with third party flights.
	MaxMemoryMib uint32 `json:"maxMemoryMib,omitzero"`

	// Timeout is the timeout for the airway instance's flight execution. Default setting is 10s.
	Timeout metav1.Duration `json:"timeout,omitzero"`
}

func (flight Flight) MarshalJSON() ([]byte, error) {
	flight.Kind = KindFlight
	flight.APIVersion = APIVersion
	type alt Flight
	return json.Marshal(alt(flight))
}

type ClusterFlight Flight

func (flight ClusterFlight) MarshalJSON() ([]byte, error) {
	flight.Kind = KindClusterFlight
	flight.APIVersion = APIVersion
	type alt ClusterFlight
	return json.Marshal(alt(flight))
}
