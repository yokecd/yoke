package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"

	"golang.org/x/term"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

type Config struct {
	Version string `json:"version"`
	Port    int    `json:"port"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg := Config{
		Version: "latest",
		Port:    3000,
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
			return fmt.Errorf("failed to decode stdin: %w", err)
		}
	}

	names := apiextensionsv1.CustomResourceDefinitionNames{
		Plural:   "airways",
		Singular: "airway",
		Kind:     "Airway",
	}

	group := "yoke.cd"

	crd := apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: "apiextensions.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Plural + "." + group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: names,
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeOf(v1alpha1.Airway{})),
					},
				},
			},
		},
	}

	account := corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-atc-service-account", flight.Release()),
			Namespace: flight.Namespace(),
		},
	}

	binding := rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-atc-cluster-role-binding", flight.Release()),
			Namespace: flight.Namespace(),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      account.Kind,
				Name:      account.Name,
				Namespace: account.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}

	labels := map[string]string{"yoke.cd/app": "atc"}

	deploment := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      flight.Release() + "-atc",
			Namespace: flight.Namespace(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: account.Name,
					Containers: []corev1.Container{
						{
							Name:  "yokecd-atc",
							Image: "davidmdm/atc:" + cfg.Version,
							Env:   []corev1.EnvVar{{Name: "PORT", Value: strconv.Itoa(cfg.Port)}},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(cfg.Port),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/live",
										Port: intstr.FromInt(cfg.Port),
									},
								},
								TimeoutSeconds: 5,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/ready",
										Port: intstr.FromInt(cfg.Port),
									},
								},
								TimeoutSeconds: 5,
							},
						},
					},
				},
			},
			Strategy: appsv1.DeploymentStrategy{Type: "Recreate"},
		},
	}

	return json.NewEncoder(os.Stdout).Encode([]any{crd, deploment, account, binding})
}

func ptr[T any](value T) *T { return &value }
