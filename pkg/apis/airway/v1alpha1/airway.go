package v1alpha1

import (
	"encoding/json"
	"reflect"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/openapi"
)

type Airway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AirwaySpec   `json:"spec"`
	Status            AirwayStatus `json:"status,omitempty"`
}

type AirwaySpec struct {
	WasmURLs         map[string]string                            `json:"wasmUrls"`
	ObjectPath       []string                                     `json:"objectPath,omitempty"`
	FixDriftInterval openapi.Duration                             `json:"fixDriftInterval,omitempty"`
	CreateCRDs       bool                                         `json:"createCrds,omitempty"`
	Template         apiextensionsv1.CustomResourceDefinitionSpec `json:"template"`
}

func (airway AirwaySpec) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	type Spec AirwaySpec
	schema := openapi.SchemaFrom(reflect.TypeFor[Spec]())
	schema.XValidations = apiextensionsv1.ValidationRules{
		{
			Rule:    "self.template.versions.map(v, v.served, v.name).all(v, v in self.wasmUrls)",
			Message: "all served versions must have a wasmUrl associated",
		},
	}
	return schema
}

type AirwayStatus struct {
	Status string `json:"status"`
	Msg    string `json:"msg"`
}

func (airway Airway) MarshalJSON() ([]byte, error) {
	airway.Kind = "Airway"
	airway.APIVersion = "yoke.cd/v1alpha1"

	type AirwayAlt Airway
	return json.Marshal(AirwayAlt(airway))
}
