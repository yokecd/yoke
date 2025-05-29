package main

import (
	"encoding/json"
	"os"

	"github.com/yokecd/yoke/pkg/flight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CR struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata"`
	Spec              flight.Status `json:"spec"`
	Status            flight.Status `json:"status"`
}

func main() {
	var cr CR
	if err := json.NewDecoder(os.Stdin).Decode(&cr); err != nil {
		panic(err)
	}

	cr.Status = cr.Spec

	json.NewEncoder(os.Stdout).Encode(cr)
}
