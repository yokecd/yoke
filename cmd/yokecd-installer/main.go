package main

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/cmd/yokecd-installer/argocd"
	"github.com/yokecd/yoke/pkg/flight"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type ContainerOpts struct {
	Resources corev1.ResourceRequirements `json:"resources"`
}

type Values struct {
	Image                string           `json:"image"`
	Version              string           `json:"version"`
	YokeCDPlugin         ContainerOpts    `json:"yokecd"`
	YokeCDServer         ContainerOpts    `json:"yokecdServer"`
	DockerAuthSecretName string           `json:"dockerAuthSecretName"`
	CacheTTL             *metav1.Duration `json:"cacheTTL"`
	ArgoCD               map[string]any   `json:"argocd"`
}

func run() error {
	values := Values{
		Image:   "ghcr.io/yokecd/yokecd",
		Version: "latest",
	}

	if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&values); err != nil && err != io.EOF {
		return fmt.Errorf("failed to decode values: %w", err)
	}

	resources, err := argocd.RenderChart(flight.Release(), flight.Namespace(), values.ArgoCD)
	if err != nil {
		return fmt.Errorf("failed to render argocd chart: %w", err)
	}

	repoServer, i := func() (*unstructured.Unstructured, int) {
		repoServerName := "argocd-repo-server"
		if flight.Release() != "argocd" {
			repoServerName = flight.Release() + "-" + repoServerName
		}
		for i, resource := range resources {
			if resource.GetName() == repoServerName && resource.GetKind() == "Deployment" {
				return resource, i
			}
		}
		return nil, -1
	}()
	if i == -1 {
		return fmt.Errorf("cannot patch argocd: failed to find argocd-repo-server deployment")
	}

	var deployment appsv1.Deployment
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(repoServer.UnstructuredContent(), &deployment); err != nil {
		return fmt.Errorf("failed to convert argocd-repo-server to typed deployment: %w", err)
	}

	plugin := corev1.Container{
		Name:            "yokecd",
		Command:         []string{"/var/run/argocd/argocd-cmp-server"},
		Image:           values.Image + ":" + values.Version,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources:       values.YokeCDPlugin.Resources,
		Env: []corev1.EnvVar{
			{
				Name:  "ARGOCD_NAMESPACE",
				Value: cmp.Or(deployment.Namespace, flight.Namespace()),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "var-files",
				MountPath: "/var/run/argocd",
			},
			{
				Name:      "plugins",
				MountPath: "/home/argocd/cmp-server/plugins",
			},
			{
				Name:      "cmp-tmp",
				MountPath: "/tmp",
			},
		},

		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: ptr(true),
			RunAsUser:    ptr[int64](999),
		},
	}

	server := corev1.Container{
		Name:            "yokecd-svr",
		Command:         []string{"yokecd", "-svr"},
		Image:           values.Image + ":" + values.Version,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: func() []corev1.EnvVar {
			var result []corev1.EnvVar
			if values.CacheTTL != nil {
				result = append(result, corev1.EnvVar{
					Name:  "YOKECD_CACHE_TTL",
					Value: values.CacheTTL.Duration.String(),
				})
			}
			return result
		}(),
		Resources: values.YokeCDServer.Resources,
		LivenessProbe: &corev1.Probe{
			PeriodSeconds:  10,
			TimeoutSeconds: 2,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt(3666),
				},
			},
		},
	}

	volumes := []corev1.Volume{
		{
			Name: "cmp-tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	if values.DockerAuthSecretName != "" {
		server.VolumeMounts = append(server.VolumeMounts, corev1.VolumeMount{
			Name:      "docker-auth-secret",
			MountPath: "/docker/config.json",
			SubPath:   ".dockerconfigjson",
		})
		server.Env = append(server.Env, corev1.EnvVar{
			Name:  "DOCKER_CONFIG",
			Value: "/docker",
		})
		volumes = append(volumes, corev1.Volume{
			Name:         "docker-auth-secret",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: values.DockerAuthSecretName}},
		})
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, plugin, server)
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumes...)

	data, err := json.Marshal(deployment)
	if err != nil {
		return err
	}

	var resource *unstructured.Unstructured
	if err := json.Unmarshal(data, &resource); err != nil {
		return err
	}

	resources[i] = resource

	return json.NewEncoder(os.Stdout).Encode(resources)
}

func ptr[T any](value T) *T { return &value }
