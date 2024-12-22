package v1alpha1

import (
	"encoding/json"

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
	WasmURLs         WasmURLs                                     `json:"wasmUrls"`
	ObjectPath       []string                                     `json:"objectPath,omitempty"`
	FixDriftInterval openapi.Duration                             `json:"fixDriftInterval,omitempty"`
	CreateCRDs       bool                                         `json:"createCrds,omitempty"`
	Template         apiextensionsv1.CustomResourceDefinitionSpec `json:"template"`
}

type WasmURLs struct {
	Flight    string `json:"flight"`
	Converter string `json:"converter,omitempty"`
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
