package main

import (
	"encoding/json"
	"log"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/pkg/flight"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(flight.Resources{
		&batchv1.Job{
			TypeMeta: metav1.TypeMeta{
				APIVersion: batchv1.SchemeGroupVersion.Identifier(),
				Kind:       "Job",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "job",
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:    "job",
								Image:   "alpine:latest",
								Command: []string{"sleep", "5"},
							},
						},
					},
				},
			},
		},
	}); err != nil {
		log.Fatalf("error: %v\n", err)
	}
}
