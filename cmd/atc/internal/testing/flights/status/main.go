package main

import (
	"encoding/json"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
)

type CRStatus struct {
	Potato     string            `json:"potato"`
	Conditions flight.Conditions `json:"conditions,omitempty"`
}

type CR struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata"`
	Spec              CRStatus `json:"spec"`
	Status            CRStatus `json:"status,omitzero"`
}

func main() {
	var cr CR
	if err := json.NewDecoder(os.Stdin).Decode(&cr); err != nil {
		panic(err)
	}

	cr.Status = cr.Spec

	json.NewEncoder(os.Stdout).Encode(cr)
}
