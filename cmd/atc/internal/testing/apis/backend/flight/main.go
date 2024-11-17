package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"

	"golang.org/x/term"

	"github.com/yokecd/yoke/pkg/flight"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"
)

var (
	release       = flight.Release()
	namespace     = flight.Namespace()
	defaultLabels = map[string]string{"app": release}
)

type Config struct {
	Image       string            `json:"image"`
	Replicas    int32             `json:"replicas"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	NodePort    int               `json:"nodePort"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var cfg Config
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
			return err
		}
	}

	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}

	maps.Copy(cfg.Labels, defaultLabels)

	return json.NewEncoder(os.Stdout).Encode([]any{
		createDeployment(cfg),
		createService(cfg),
	})
}

func createDeployment(cfg Config) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      release,
			Namespace: namespace,
			Labels:    cfg.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &cfg.Replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{MatchLabels: cfg.Labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: cfg.Labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            release,
							Image:           cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{
									Name:  "PORT",
									Value: strconv.Itoa(3000),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          release,
									Protocol:      corev1.ProtocolTCP,
									ContainerPort: 3000,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createService(cfg Config) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.Identifier(),
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      release,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: defaultLabels,
			Type: func() corev1.ServiceType {
				if cfg.NodePort > 0 {
					return corev1.ServiceTypeNodePort
				}
				return corev1.ServiceTypeClusterIP
			}(),
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					NodePort:   int32(cfg.NodePort),
					Port:       80,
					TargetPort: intstr.FromString(release),
				},
			},
		},
	}
}
