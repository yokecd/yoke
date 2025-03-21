package v1

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	APIVersion  = "examples.com/v1"
	KindBackend = "Backend"
)

type Backend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              BackendSpec `json:"spec"`
}

type BackendSpec struct {
	Image       string            `json:"image"`
	Replicas    int32             `json:"replicas"`
	Labels      map[string]string `json:"labels,omitempty"`
	NodePort    int               `json:"nodePort,omitempty"`
	ServicePort int               `json:"port,omitempty"`
}

func (backend Backend) MarshalJSON() ([]byte, error) {
	backend.Kind = KindBackend
	backend.APIVersion = APIVersion

	type BackendAlt Backend
	return json.Marshal(BackendAlt(backend))
}

func (backend *Backend) UnmarshalJSON(data []byte) error {
	type BackendAlt Backend
	if err := json.Unmarshal(data, (*BackendAlt)(backend)); err != nil {
		return err
	}
	if backend.APIVersion != APIVersion {
		return fmt.Errorf("unexpected api version: expected %s but got %s", APIVersion, backend.APIVersion)
	}
	return nil
}
