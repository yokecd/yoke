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

// TODO:
// 1) wasmURL should be map between the crd version and the wasmURL.
// 2) We should have a field to define the path of the object we want to serialize as the input to the flight otherwise defaulting to root.
type AirwaySpec struct {
	WasmURL          string                                       `json:"wasmUrl"`
	ObjectPath       []string                                     `json:"objectPath,omitempty"`
	Template         apiextensionsv1.CustomResourceDefinitionSpec `json:"template"`
	FixDriftInterval openapi.Duration                             `json:"fixDriftInterval,omitempty"`
	CreateCRDs       bool                                         `json:"createCrds,omitempty"`
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
