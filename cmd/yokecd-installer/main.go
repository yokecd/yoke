package main

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	jyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/cmd/yokecd-installer/argocd"
	"github.com/yokecd/yoke/internal/xcrypto"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type ContainerOpts struct {
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`
}

type YokeCDServer struct {
	ContainerOpts
	CacheFS string `json:"cacheFS,omitzero"`
}

type Values struct {
	Image                  string         `json:"image,omitzero" Description:"yokecd image"`
	Version                string         `json:"version,omitzero" Description:"yokecd image version"`
	YokeCDPlugin           ContainerOpts  `json:"yokecd,omitzero"`
	YokeCDServer           YokeCDServer   `json:"yokecdServer,omitzero"`
	DockerAuthSecretName   string         `json:"dockerAuthSecretName,omitzero" Description:"dockerconfig secret for pulling wasm modules from private oci registries"`
	ArgoCD                 map[string]any `json:"argocd,omitzero" Description:"arguments passed to ArgoCD helm chart"`
	ModuleAllowList        []string       `json:"moduleAllowList,omitzero" Description:"list of patterns that define the module allow-list. If empty all modules are allowed."`
	ModuleVerificationKeys []string       `json:"moduleVerificationKeys,omitzero" Description:"list of public keys uses to verify modules. Allowlist takes precedence."`
}

func run() error {
	schema := flag.Bool("schema", false, "show input schema")
	flag.Parse()

	if *schema {
		return encodeAsYaml(os.Stdout, openapi.SchemaFor[Values]())
	}

	cfg := Values{
		Image:   "ghcr.io/yokecd/yokecd",
		Version: "latest",
	}

	if err := jyaml.NewYAMLToJSONDecoder(os.Stdin).Decode(&cfg); err != nil && err != io.EOF {
		return fmt.Errorf("failed to decode values: %w", err)
	}

	resources, deployment, err := func() (flight.Resources, *appsv1.Deployment, error) {
		resources, err := argocd.RenderChart(flight.Release(), flight.Namespace(), cfg.ArgoCD)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to render argocd chart: %w", err)
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
			return nil, nil, fmt.Errorf("cannot patch argocd: failed to find argocd-repo-server deployment")
		}

		var deployment appsv1.Deployment
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(repoServer.UnstructuredContent(), &deployment); err != nil {
			return nil, nil, fmt.Errorf("failed to convert argocd-repo-server to typed deployment: %w", err)
		}

		var result flight.Resources
		for _, resource := range slices.Delete(resources, i, i+1) {
			result = append(result, resource)
		}

		result = append(result, &deployment)

		return result, &deployment, nil
	}()
	if err != nil {
		return err
	}

	plugin := corev1.Container{
		Name:            "yokecd",
		Command:         []string{"/var/run/argocd/argocd-cmp-server"},
		Image:           cfg.Image + ":" + cfg.Version,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources:       cfg.YokeCDPlugin.Resources,
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
			RunAsNonRoot: new(true),
			RunAsUser:    ptr.To[int64](999),
		},
	}

	if len(cfg.ModuleAllowList) > 0 {
		plugin.Env = append(plugin.Env, corev1.EnvVar{Name: "MODULE_ALLOW_LIST", Value: strings.Join(cfg.ModuleAllowList, ",")})
	}

	cacheFS := cmp.Or(cfg.YokeCDServer.CacheFS, "/tmp")

	server := corev1.Container{
		Name:            "yokecd-svr",
		Command:         []string{"yokecd", "-svr"},
		Image:           cfg.Image + ":" + cfg.Version,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{
				Name:  "YOKECD_CACHE_FS",
				Value: cacheFS,
			},
		},
		Resources: cfg.YokeCDServer.Resources,
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
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "yokecd-cache",
				MountPath: cacheFS,
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
		{
			Name: "yokecd-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	if cfg.DockerAuthSecretName != "" {
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
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: cfg.DockerAuthSecretName}},
		})
	}

	keySecret, err := func() (*corev1.Secret, error) {
		if len(cfg.ModuleVerificationKeys) == 0 {
			return nil, nil
		}
		data, err := json.Marshal(cfg.ModuleVerificationKeys)
		if err != nil {
			return nil, fmt.Errorf("could not marshal module verification keys: %w", err)
		}
		// Just validate that they are parseable.
		var keys xcrypto.PublicKeySet
		if err := json.Unmarshal(data, &keys); err != nil {
			return nil, fmt.Errorf("invalid module verification keys: %w", err)
		}

		return &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Name: flight.Release() + "-module-verification-keys"},
			Immutable:  new(true),
			Data:       map[string][]byte{"keys.json": data},
			Type:       corev1.SecretTypeOpaque,
		}, nil
	}()
	if err != nil {
		return err
	}

	resources = append(resources, keySecret)

	if keySecret != nil {
		var (
			volume = corev1.Volume{
				Name:         "module-verification-keys",
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: keySecret.Name}},
			}
			mount = corev1.VolumeMount{
				Name:      volume.Name,
				MountPath: "/var/run/plugin-svr/keys",
				ReadOnly:  true,
			}
			path = path.Join(mount.MountPath, "keys.json")
		)

		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volume)
		server.VolumeMounts = append(server.VolumeMounts, mount)
		server.Env = append(server.Env, corev1.EnvVar{Name: "MODULE_VERIFICATION_KEYS_PATH", Value: path})
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, plugin, server)
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumes...)

	return json.NewEncoder(os.Stdout).Encode(resources)
}

func encodeAsYaml(dst io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	return yaml.NewEncoder(dst).Encode(obj)
}
