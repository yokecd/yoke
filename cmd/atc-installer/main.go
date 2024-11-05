package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/term"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/pkg/flight"
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
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:     "object",
							Required: []string{"spec"},
							Properties: apiextensionsv1.JSONSchemaDefinitions{
								"spec": apiextensionsv1.JSONSchemaProps{
									Type:     "object",
									Required: []string{"wasmUrl", "template"},
									Properties: apiextensionsv1.JSONSchemaDefinitions{
										"wasmUrl": {
											Type: "string",
										},
										"createCrds": {
											Type: "boolean",
										},
										"template": crdSpecSchema,
									},
								},
								"status": apiextensionsv1.JSONSchemaProps{
									Type:     "object",
									Required: []string{"Status", "Msg"},
									Properties: apiextensionsv1.JSONSchemaDefinitions{
										"Status": {Type: "string"},
										"Msg":    {Type: "string"},
									},
								},
							},
						},
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

var crdSpecSchema = apiextensionsv1.JSONSchemaProps{
	Type:     "object",
	Required: []string{"group", "names", "scope", "versions"},
	Properties: map[string]apiextensionsv1.JSONSchemaProps{
		"group": {
			Type:        "string",
			Description: "The API group for the CRD, e.g., 'mygroup.example.com'.",
		},
		"names": {
			Type:     "object",
			Required: []string{"kind", "plural"},
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"kind": {
					Type:        "string",
					Description: "The kind name for the custom resource, e.g., 'MyResource'.",
				},
				"plural": {
					Type:        "string",
					Description: "The plural name of the custom resource, used in the URL path, e.g., 'myresources'.",
				},
				"singular": {
					Type:        "string",
					Description: "Singular name for the custom resource, e.g., 'myresource'.",
				},
				"shortNames": {
					Type:        "array",
					Description: "Optional short names for the resource.",
					Items: &apiextensionsv1.JSONSchemaPropsOrArray{
						Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"},
					},
				},
				"categories": {
					Type:        "array",
					Description: "Optional list of categories for the resource.",
					Items: &apiextensionsv1.JSONSchemaPropsOrArray{
						Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"},
					},
				},
			},
		},
		"scope": {
			Type:        "string",
			Description: "Defines whether the CRD is namespaced ('Namespaced') or cluster-wide ('Cluster').",
			Enum: []apiextensionsv1.JSON{
				{Raw: []byte(`"Namespaced"`)},
				{Raw: []byte(`"Cluster"`)},
			},
		},
		"versions": {
			Type:        "array",
			Description: "List of all versions for this custom resource.",
			Items: &apiextensionsv1.JSONSchemaPropsOrArray{
				Schema: &apiextensionsv1.JSONSchemaProps{
					Type:     "object",
					Required: []string{"name", "served", "storage"},
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"name": {
							Type:        "string",
							Description: "The version name, e.g., 'v1alpha1'.",
						},
						"served": {
							Type:        "boolean",
							Description: "Whether the version is served by the API server.",
						},
						"storage": {
							Type:        "boolean",
							Description: "Whether the version is used as the storage version.",
						},
						"schema": {
							Type:        "object",
							Description: "Schema for validation of custom resource instances.",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"openAPIV3Schema": {
									Type:                   "object",
									Description:            "OpenAPI v3 schema for custom resource validation.",
									XPreserveUnknownFields: func(value bool) *bool { return &value }(true),
								},
							},
						},
						"subresources": {
							Type:        "object",
							Description: "Specifies the status and scale subresources for the custom resource.",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"status": {
									Type:        "object",
									Description: "Enables the status subresource for custom resources.",
								},
								"scale": {
									Type:        "object",
									Description: "Enables the scale subresource for custom resources.",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"specReplicasPath": {
											Type:        "string",
											Description: "JSON path to the replicas field in the custom resource.",
										},
										"statusReplicasPath": {
											Type:        "string",
											Description: "JSON path to the replicas field in the custom resource's status.",
										},
										"labelSelectorPath": {
											Type:        "string",
											Description: "JSON path to a label selector in the custom resource.",
										},
									},
								},
							},
						},
						"additionalPrinterColumns": {
							Type:        "array",
							Description: "Additional columns to display when listing the custom resource.",
							Items: &apiextensionsv1.JSONSchemaPropsOrArray{
								Schema: &apiextensionsv1.JSONSchemaProps{
									Type:     "object",
									Required: []string{"name", "type", "jsonPath"},
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"name": {
											Type:        "string",
											Description: "Name of the column.",
										},
										"type": {
											Type:        "string",
											Description: "Type of the column (e.g., 'integer', 'string').",
										},
										"description": {
											Type:        "string",
											Description: "Description of the column.",
										},
										"jsonPath": {
											Type:        "string",
											Description: "JSON path to retrieve the value from the custom resource.",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	},
}

func ptr[T any](value T) *T { return &value }
