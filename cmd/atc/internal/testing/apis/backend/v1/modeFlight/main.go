package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	v1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var backend v1.Backend
	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&backend); err != nil && err != io.EOF {
		return err
	}

	backend.Spec.ServicePort = cmp.Or(backend.Spec.ServicePort, 3000)

	if backend.Spec.Labels == nil {
		backend.Spec.Labels = map[string]string{}
	}

	maps.Copy(backend.Spec.Labels, selector(backend))

	cm, err := createConfigMap(backend)
	if err != nil {
		return fmt.Errorf("failed to create configmap: %v", err)
	}

	// the configmap will allow us to test dynamic modes by overriding replicas via the configmap.
	if cm != nil {
		if replicas, err := strconv.Atoi(cm.Data["replicas"]); err == nil && replicas > 0 {
			backend.Spec.Replicas = int32(replicas)
		}
	}

	return json.NewEncoder(os.Stdout).Encode(flight.Resources{
		cm,
		createDeployment(backend),
		createService(backend),
	})
}

func createConfigMap(backend v1.Backend) (*corev1.ConfigMap, error) {
	cm, err := k8s.Lookup[corev1.ConfigMap](k8s.ResourceIdentifier{
		Name:       backend.Name,
		Namespace:  backend.Namespace,
		Kind:       "ConfigMap",
		ApiVersion: "v1",
	})
	if err != nil && !k8s.IsErrNotFound(err) {
		if errors.Is(err, k8s.ErrorClusterAccessNotGranted) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to lookup configmap: %v", err)
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backend.Name,
			Namespace: backend.Namespace,
		},
		Data: func() map[string]string {
			if cm != nil {
				return cm.Data
			}
			return map[string]string{}
		}(),
	}, nil
}

func createDeployment(backend v1.Backend) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backend.Name,
			Namespace: backend.Namespace,
			Labels:    backend.Spec.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &backend.Spec.Replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{MatchLabels: selector(backend)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: backend.Spec.Labels},
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

func createService(backend v1.Backend) *corev1.Service {
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

func selector(backend v1.Backend) map[string]string {
	return map[string]string{"app": backend.Name}
}
