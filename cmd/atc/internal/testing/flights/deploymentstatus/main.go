package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

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
	Image             string        `json:"image"`
	Status            flight.Status `json:"status,omitzero"`
}

func run() error {
	var cr CR
	if err := json.NewDecoder(os.Stdin).Decode(&cr); err != nil {
		return err
	}

	selector := map[string]string{
		"app.kubernetes.io/name": cr.Name,
	}

	deployment := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: cr.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: cr.Image,
						},
					},
				},
			},
		},
	}

	cr.Status = flight.Status{
		Props: map[string]any{
			"availablePods": func() string {
				dep, err := k8s.Lookup[appsv1.Deployment](k8s.ResourceIdentifier{
					Name:       deployment.Name,
					Namespace:  flight.Namespace(),
					Kind:       deployment.Kind,
					ApiVersion: deployment.APIVersion,
				})
				if err != nil && !k8s.IsErrNotFound(err) {
					panic(err)
				}
				if dep == nil {
					return "unknown"
				}
				return strconv.Itoa(int(dep.Status.AvailableReplicas))
			}(),
		},
	}

	return json.NewEncoder(os.Stdout).Encode(flight.Resources{
		&cr,
		&deployment,
	})
}
