// This program serves to test that a dynamic flight can run into an error condition,
// and update the parent (identity) resource without affecting the other resources in the release.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type CR struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata"`
	Status            struct {
		Message string `json:"message,omitempty"`
	} `json:"status"`
}

func run() error {
	var parent CR
	if err := json.NewDecoder(os.Stdin).Decode(&parent); err != nil {
		return err
	}

	deployment := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: parent.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "cats"},
			},
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "cats"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "yokecd/c4ts:latest",
						},
					},
				},
			},
		},
	}

	resources := flight.Resources{&parent, &deployment}

	if _, err := k8s.LookupResource(&deployment); err != nil {
		if k8s.IsErrNotFound(err) {
			parent.Status.Message = "deploying workload..."
			return json.NewEncoder(os.Stdout).Encode(resources)
		}
		parent.Status.Message = "failed to lookup deployment: " + err.Error()
		return flight.JSONEncodeWithError(os.Stdout, parent, err)
	}

	parent.Status.Message = "artificial test error"
	return flight.JSONEncodeWithError(os.Stdout, parent, errors.New("generic error"))
}
