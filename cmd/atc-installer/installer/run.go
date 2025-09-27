package installer

import (
	"cmp"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/flight/wasi/k8s"
)

type Config struct {
	Labels                 map[string]string `json:"labels"`
	Annotations            map[string]string `json:"annotations"`
	Image                  string            `json:"image"`
	Version                string            `json:"version"`
	Port                   int               `json:"port"`
	ServiceAccountName     string            `json:"serviceAccountName"`
	ImagePullPolicy        corev1.PullPolicy `json:"ImagePullPolicy"`
	GenerateTLS            bool              `json:"generateTLS"`
	DockerConfigSecretName string            `json:"dockerConfigSecretName"`
	LogFormat              string            `json:"logFormat"`
}

var (
	group = "yoke.cd"
	names = apiextensionsv1.CustomResourceDefinitionNames{
		Plural:   "airways",
		Singular: "airway",
		Kind:     "Airway",
	}
)

func Run(cfg Config) (flight.Resources, error) {
	account, binding := func() (*corev1.ServiceAccount, *rbacv1.ClusterRoleBinding) {
		if cfg.ServiceAccountName != "" {
			return nil, nil
		}
		account := &corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ServiceAccount",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-atc-service-account", flight.Release()),
				Namespace: flight.Namespace(),
			},
		}

		binding := &rbacv1.ClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterRoleBinding",
				APIVersion: rbacv1.SchemeGroupVersion.Identifier(),
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

		return account, binding
	}()

	selector := map[string]string{
		"yoke.cd/app": "atc",
	}

	labels := map[string]string{}
	maps.Copy(labels, cfg.Labels)

	maps.Copy(labels, selector)

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      flight.Release() + "-atc",
			Namespace: flight.Namespace(),
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(cfg.Port),
				},
			},
		},
	}

	const (
		keyRootCA     = "ca.crt"
		keyServerCert = "server.crt"
		keyServerKey  = "server.key"
	)

	tls, err := func() (*TLS, error) {
		if cfg.GenerateTLS {
			return NewTLS(svc)
		}
		secret, err := k8s.Lookup[corev1.Secret](k8s.ResourceIdentifier{
			Name:       flight.Release() + "-tls",
			Namespace:  flight.Namespace(),
			Kind:       "Secret",
			ApiVersion: "v1",
		})
		if err != nil {
			if !k8s.IsErrNotFound(err) && !errors.Is(err, k8s.ErrorClusterAccessNotGranted) {
				return nil, fmt.Errorf("failed to lookup tls secret: %T: %v", err, err)
			}

			if errors.Is(err, k8s.ErrorClusterAccessNotGranted) {
				fmt.Fprintln(os.Stderr, "Cluster-access not granted: enable cluster-access to reuse existing TLS certificates.")
			}
		}
		if secret != nil {
			return &TLS{
				RootCA:     secret.Data[keyRootCA],
				ServerCert: secret.Data[keyServerCert],
				ServerKey:  secret.Data[keyServerKey],
			}, nil
		}
		return NewTLS(svc)
	}()
	if err != nil {
		return nil, err
	}

	tlsSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      flight.Release() + "-tls",
			Namespace: flight.Namespace(),
		},
		Data: map[string][]byte{
			keyRootCA:     tls.RootCA,
			keyServerCert: tls.ServerCert,
			keyServerKey:  tls.ServerKey,
		},
	}

	labels["yoke.cd/dependency-hash"] = func() string {
		hash := sha1.New()
		for _, key := range slices.Sorted(maps.Keys(tlsSecret.Data)) {
			hash.Write(tlsSecret.Data[key])
		}
		return hex.EncodeToString(hash.Sum(nil))
	}()

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      flight.Release() + "-atc",
			Namespace: flight.Namespace(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: cfg.Annotations,
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "tls-secrets",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{SecretName: tlsSecret.GetName()},
							},
						},
					},
					ServiceAccountName: func() string {
						if cfg.ServiceAccountName != "" {
							return cfg.ServiceAccountName
						}
						return account.Name
					}(),
					Containers: []corev1.Container{
						{
							Name:            "yokecd-atc",
							Image:           cmp.Or(cfg.Image, "ghcr.io/yokecd/atc") + ":" + cfg.Version,
							ImagePullPolicy: cmp.Or(cfg.ImagePullPolicy, corev1.PullIfNotPresent),
							Env: []corev1.EnvVar{
								{Name: "PORT", Value: strconv.Itoa(cfg.Port)},
								{Name: "TLS_CA_CERT", Value: "/conf/tls/ca.crt"},
								{Name: "TLS_SERVER_CERT", Value: "/conf/tls/server.crt"},
								{Name: "TLS_SERVER_KEY", Value: "/conf/tls/server.key"},
								{Name: "SVC_NAME", Value: svc.Name},
								{Name: "SVC_NAMESPACE", Value: svc.Namespace},
								{Name: "SVC_PORT", Value: strconv.Itoa(int(svc.Spec.Ports[0].Port))},
								{Name: "DOCKER_CONFIG_SECRET_NAME", Value: cfg.DockerConfigSecretName},
								{Name: "LOG_FORMAT", Value: cfg.LogFormat},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "tls-secrets",
									ReadOnly:  true,
									MountPath: "/conf/tls",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(cfg.Port),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/live",
										Port:   intstr.FromInt(cfg.Port),
										Scheme: corev1.URISchemeHTTPS,
									},
								},
								TimeoutSeconds: 5,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/ready",
										Port:   intstr.FromInt(cfg.Port),
										Scheme: corev1.URISchemeHTTPS,
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

	return flight.Resources{
		svc,
		tlsSecret,
		deployment,
		account,
		binding,
	}, nil
}
