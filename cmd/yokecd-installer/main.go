package main

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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

type Values struct {
	Version string         `json:"version"`
	ArgoCD  map[string]any `json:"argocd"`
}

func run() error {
	values := Values{Version: "latest"}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if err := yaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&values); err != nil && err != io.EOF {
			return fmt.Errorf("failed to decode values: %w", err)
		}
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
			if resource.GetKind() == "Deployment" {
				fmt.Fprintf(os.Stderr, "did not match deployment: %s != %s\n", resource.GetName(), repoServerName)
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

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
		Name:            "yokecd",
		Command:         []string{"/var/run/argocd/argocd-cmp-server"},
		Image:           "ghcr.io/yokecd/yokecd:" + values.Version,
		ImagePullPolicy: corev1.PullIfNotPresent,

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
	})

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "cmp-tmp",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

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
