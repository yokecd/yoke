package v1alpha1

import (
	"encoding/json"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Airway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		WasmURL    string                                       `json:"wasmUrl"`
		CreateCRDs bool                                         `json:"createCrds,omitempty"`
		Template   apiextensionsv1.CustomResourceDefinitionSpec `json:"template"`
	} `json:"spec"`
	Status struct {
		Status string
		Msg    string
	} `json:"status,omitempty"`
}

func (airway Airway) MarshalJSON() ([]byte, error) {
	airway.Kind = "Airway"
	airway.APIVersion = "yoke.cd/v1alpha1"

	type AirwayAlt Airway
	return json.Marshal(AirwayAlt(airway))
}
