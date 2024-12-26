package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"

	"golang.org/x/term"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	v2 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var backend v2.Backend
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&backend); err != nil && err != io.EOF {
			return err
		}
	}

	backend.Spec.ServicePort = cmp.Or(backend.Spec.ServicePort, 3000)

	if backend.Spec.Meta.Labels == nil {
		backend.Spec.Meta.Labels = map[string]string{}
	}

	maps.Copy(backend.Spec.Meta.Labels, selector(backend))

	return json.NewEncoder(os.Stdout).Encode([]any{
		createDeployment(backend),
		createService(backend),
	})
}

func createDeployment(backend v2.Backend) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backend.Name,
			Namespace: backend.Namespace,
			Labels:    backend.Spec.Meta.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &backend.Spec.Replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{MatchLabels: selector(backend)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      backend.Spec.Meta.Labels,
					Annotations: backend.Spec.Meta.Annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            backend.Name,
							Image:           backend.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{
									Name:  "PORT",
									Value: strconv.Itoa(backend.Spec.ServicePort),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          backend.Name,
									Protocol:      corev1.ProtocolTCP,
									ContainerPort: int32(backend.Spec.ServicePort),
								},
							},
						},
					},
				},
			},
		},
	}
}

func createService(backend v2.Backend) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.Identifier(),
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backend.Name,
			Namespace: backend.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: selector(backend),
			Type: func() corev1.ServiceType {
				if backend.Spec.NodePort > 0 {
					return corev1.ServiceTypeNodePort
				}
				return corev1.ServiceTypeClusterIP
			}(),
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					NodePort:   int32(backend.Spec.NodePort),
					Port:       80,
					TargetPort: intstr.FromString(backend.Name),
				},
			},
		},
	}
}

func selector(backend v2.Backend) map[string]string {
	return map[string]string{"app": backend.Name}
}
