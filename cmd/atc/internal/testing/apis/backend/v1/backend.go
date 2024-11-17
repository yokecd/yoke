package v1

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Backend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackendSpec `json:"spec"`
}

type BackendSpec struct {
	Image    string            `json:"image"`
	Replicas int32             `json:"replicas"`
	Labels   map[string]string `json:"labels,omitempty"`
	NodePort int               `json:"nodePort,omitempty"`
}

func (backend Backend) MarshalJSON() ([]byte, error) {
	backend.Kind = "Backend"
	backend.APIVersion = "examples.com/v1"

	type BackendAlt Backend
	return json.Marshal(BackendAlt(backend))
}
