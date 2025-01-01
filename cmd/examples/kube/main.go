package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	name := "sample-app"
	labels := map[string]string{"app": name}

	flag.Parse()

	replicas, _ := strconv.Atoi(flag.Arg(0))
	if replicas == 0 {
		replicas = 2
	}

	dep := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(replicas)),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    name,
							Image:   "alpine:latest",
							Command: []string{"watch", "echo", "hello", "world"},
						},
					},
				},
			},
		},
	}

	return json.NewEncoder(os.Stdout).Encode([]any{dep})
}

func ptr[T any](value T) *T { return &value }
