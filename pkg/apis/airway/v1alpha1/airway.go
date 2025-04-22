package v1alpha1

import (
	"encoding/json"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/pkg/flight"
)

const (
	KindAirway = "Airway"
	APIVersion = "yoke.cd/v1alpha1"
)

type Airway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              AirwaySpec    `json:"spec"`
	Status            flight.Status `json:"status,omitzero"`
}

type AirwayMode string

func (AirwayMode) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	return &apiextensionsv1.JSONSchemaProps{
		Type: "string",
		Enum: func() []apiextensionsv1.JSON {
			var result []apiextensionsv1.JSON
			for _, value := range Modes() {
				data, _ := json.Marshal(value)
				result = append(result, apiextensionsv1.JSON{Raw: data})
			}
			return result
		}(),
	}
}

const (
	AirwayModeStandard AirwayMode = "standard"
	AirwayModeStatic   AirwayMode = "static"
	AirwayModeDynamic  AirwayMode = "dynamic"
)

// Modes returns the list of known Airway modes.
func Modes() []AirwayMode {
	return []AirwayMode{AirwayModeStandard, AirwayModeStatic, AirwayModeDynamic}
}

type AirwaySpec struct {
	// WasmURLs defines the locations for the various implementations the AirTrafficController will invoke.
	WasmURLs WasmURLs `json:"wasmUrls"`

	// ObjectPath allows you to set a path within your CR to the value you wish to pass to your flight.
	// By default the entire Custom Resource is injected via STDIN to your flight implementation.
	// If, for example, you wish to encode the "spec" property over STDIN you would set ObjectPath to []string{"spec"}.
	ObjectPath []string `json:"objectPath,omitempty"`

	// FixDriftInterval sets an interval at which the resource is requeued for evaluation by the AirTrafficController.
	// The ATC will attempt to reapply the resource. In most cases this will result in a noop. If however a user
	// changed any of the underlying resource's configuration, this will be set back via this mechanism. It allows
	// you to enforce the desired state of your resource against external manipulation.
	FixDriftInterval metav1.Duration `json:"fixDriftInterval,omitzero"`

	// ClusterAccess allows the flight to lookup resources in the cluster. Resources are limited to those owned by the calling release.
	ClusterAccess bool `json:"clusterAccess,omitempty"`

	// CrossNamespace allows for resources to be created in other namespaces other than the releases target namespace.
	CrossNamespace bool `json:"crossNamespace,omitempty"`

	// Insecure only applies to flights using OCI urls. Allows image references to be fetched without TLS verification.
	Insecure bool `json:"insecure,omitempty"`

	// SkipAdmissionWebhook bypasses admission webhook for the airway's CRs.
	// The admission webhook validates that the resources that would be created pass a dry-run phase.
	// However in the case of some multi-stage implementations, stages that depend on prior stages cannot pass dry-run.
	// In this case there is no option but to skip the admission webhook.
	//
	// Therefore multi-stage Airways are not generally recommended.
	SkipAdmissionWebhook bool `json:"skipAdmissionWebhook,omitempty"`

	// Mode sets different behaviors for how the child resources of flights are managed by the ATC.
	//
	// - "standard" is the same not specifying any mode. In "standard" mode, flights are evaluated once
	// and child resources are applied, and no further evaluation is made should child resources be modified.
	//
	// - "static" mode checks any change to a child resource against desired state at admission time.
	// If any fields conflict with the desired state the change is rejected at admission.
	//
	// - "dynamic" mode requeues the parent flight for evaluation any time a child resource is modified.
	// This means that if a conflicting state is found it will be reverted in realtime.
	// The advantage of this mode over static, is that combined with cluster-access we can dynamically
	// build new desired state based on external changes to child resources.
	Mode AirwayMode `json:"mode,omitempty"`

	// HistoryCapSize controls how many revisions of an instance (custom resource) of this airway is kept in history.
	// To make it uncapped set this value to any negative integer.
	// By default 2.
	HistoryCapSize int `json:"historyCapSize,omitempty"`

	// Template is the CustomResourceDefinition Specification to create. A CRD will be created using this specification
	// and bound to the implementation defined by the WasmURLs.Flight property.
	Template apiextensionsv1.CustomResourceDefinitionSpec `json:"template"`
}

type WasmURLs struct {
	// Flight is the implementation used to implement the CustomResource as a Package. The flight is always applied against
	// the storage version of the Custom Resource. This property is required.
	Flight string `json:"flight"`

	// Converter is the implementation of the conversion webhook. If present, the ATC will automatically use it to serve conversion
	// requests between the various served versions of the Custom Resource.
	Converter string `json:"converter,omitempty"`
}

func (airway Airway) MarshalJSON() ([]byte, error) {
	airway.Kind = KindAirway
	airway.APIVersion = APIVersion

	type AirwayAlt Airway
	return json.Marshal(AirwayAlt(airway))
}

// CRD returns the CustomResourceDefinition as described by the template. The CRD will share the same name as the Airway and is owned by it.
func (airway Airway) CRD() apiextensionsv1.CustomResourceDefinition {
	return apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: airway.Name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         airway.APIVersion,
					Kind:               airway.Kind,
					Name:               airway.Name,
					UID:                airway.UID,
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: airway.Spec.Template,
	}
}

// CRGroupResource returns the schema.GroupResource of the Custom Resource as defined by its CRD template.
func (airway Airway) CRGroupResource() schema.GroupResource {
	return schema.GroupResource{
		Group:    airway.Spec.Template.Group,
		Resource: airway.Spec.Template.Names.Plural,
	}
}
