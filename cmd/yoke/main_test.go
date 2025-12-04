package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestMain(m *testing.M) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(x.X("kind delete cluster --name=yoke-cli-tests"))
	must(x.X("kind create cluster --name=yoke-cli-tests"))

	os.Exit(m.Run())
}

var (
	settings = GlobalSettings{
		Kube: func() *genericclioptions.ConfigFlags {
			flags := genericclioptions.NewConfigFlags(false)
			flags.KubeConfig = &home.Kubeconfig
			return flags
		}(),
	}
	background = internal.WithStdio(context.Background(), io.Discard, io.Discard, nil)
)

func createBasicDeployment(t *testing.T, name, namespace string) io.Reader {
	labels := map[string]string{"app": name}
	deployment := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.Identifier(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
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

	data, err := json.Marshal(deployment)
	require.NoError(t, err)

	return bytes.NewReader(data)
}

func TestCreateEmptyDeployment(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: bytes.NewReader(nil),
			},
		},
	}

	restcfg, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(restcfg)
	require.NoError(t, err)

	client, err := k8s.NewClient(restcfg, "")
	require.NoError(t, err)

	revisions, err := client.GetReleases(background)
	require.NoError(t, err)
	require.Len(t, revisions, 0)

	defaultDeployments := clientset.AppsV1().Deployments("default")

	deployments, err := defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)

	require.EqualError(t, TakeOff(background, params), "failed to takeoff: resource provided is either empty or invalid")

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)
	// Test cleanup in case a foo release already exists (best-effort)
	Mayday(background, MaydayParams{
		GlobalSettings: settings,
		MaydayParams: yoke.MaydayParams{
			Release: "foo",
		},
	})
}

func TestCreateThenEmptyCycle(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "sample-app", "default"),
			},
		},
	}

	restcfg, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(restcfg)
	require.NoError(t, err)

	client, err := k8s.NewClient(restcfg, "")
	require.NoError(t, err)

	revisions, err := client.GetReleases(background)
	require.NoError(t, err)
	require.Len(t, revisions, 0)

	defaultDeployments := clientset.AppsV1().Deployments("default")

	deployments, err := defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)

	require.NoError(t, TakeOff(background, params))

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 1)

	require.EqualError(t, TakeOff(background, TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: bytes.NewReader(nil),
			},
		},
	}), "failed to takeoff: resource provided is either empty or invalid")

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 1)
	// Test cleanup in case a foo release already exists (best-effort)
	Mayday(background, MaydayParams{
		GlobalSettings: settings,
		MaydayParams: yoke.MaydayParams{
			Release: "foo",
		},
	})
}

func TestCreateDeleteCycle(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "sample-app", "default"),
			},
		},
	}

	restcfg, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(restcfg)
	require.NoError(t, err)

	client, err := k8s.NewClient(restcfg, "")
	require.NoError(t, err)

	revisions, err := client.GetReleases(background)
	require.NoError(t, err)
	require.Len(t, revisions, 0)

	defaultDeployments := clientset.AppsV1().Deployments("default")

	deployments, err := defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)

	require.NoError(t, TakeOff(background, params))

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 1)

	require.NoError(t, Mayday(background, MaydayParams{
		GlobalSettings: settings,
		MaydayParams: yoke.MaydayParams{
			Release: "foo",
		},
	}))

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)
}

func TestCreateWithWait(t *testing.T) {
	params := func(timeout time.Duration) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release: "foo",
				Flight: yoke.FlightParams{
					Input: createBasicDeployment(t, "sample-app", "default"),
				},
				Wait: timeout,
			},
		}
	}

	mayday := func() error {
		return Mayday(background, MaydayParams{
			GlobalSettings: settings,
			MaydayParams: yoke.MaydayParams{
				Release: "foo",
			},
		})
	}

	// Test cleanup in case a foo release already exists (best-effort)
	_ = mayday()

	require.NoError(t, TakeOff(background, params(30*time.Second)))

	require.NoError(t, mayday())

	err := TakeOff(background, params(1*time.Nanosecond))
	require.Error(t, err, "expected an error")

	// Expectation split into two to remove flakiness. The context being canceled can trigger errors from different places
	// either directly within yoke or within client-go, hence we capture the cause and the top level message only
	require.Contains(t, err.Error(), "release did not become ready within wait period: to rollback use `yoke descent`: failed to get readiness for default/apps/v1/deployment/sample-app")
	require.Contains(t, err.Error(), "1ns timeout reached")
}

func TestFailApplyDryRun(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:         "foo",
			CreateNamespace: true,
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "%invalid-chars&*", "default"),
			},
		},
	}

	require.ErrorContains(
		t,
		TakeOff(background, params),
		`failed to apply resources: dry run: default/apps/v1/deployment/%invalid-chars&*: failed to validate resource release`,
	)
}

func TestMultiNamespaceValidation(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	for _, ns := range []string{"alpha", "beta"} {
		require.NoError(t, client.EnsureNamespace(context.Background(), ns))
		defer func() {
			require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}))
		}()
	}

	makeParams := func(multiNamespace bool) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:        "foo",
				CrossNamespace: multiNamespace,
				Flight: yoke.FlightParams{
					Input: strings.NewReader(`[
            {
              apiVersion: v1,
              kind: ConfigMap,
              metadata: {
                name: alpha,
                namespace: alpha,
              },
              data: {},
            },
            {
              apiVersion: v1,
              kind: ConfigMap,
              metadata: {
                name: beta,
                namespace: beta,
              },
              data: {},
            },
          ]`),
				},
			},
		}
	}

	err = TakeOff(context.Background(), makeParams(false))
	require.ErrorContains(t, err, "Multiple namespaces detected")
	require.ErrorContains(t, err, `namespace "alpha" does not match target namespace "default"`)
	require.ErrorContains(t, err, `namespace "beta" does not match target namespace "default"`)

	require.NoError(t, TakeOff(context.Background(), makeParams(true)))
	require.NoError(t, Mayday(context.Background(), MaydayParams{
		MaydayParams:   yoke.MaydayParams{Release: "foo"},
		GlobalSettings: settings,
	}))
}

func TestReleaseOwnership(t *testing.T) {
	makeParams := func(name string) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release: name,
				Flight: yoke.FlightParams{
					Input: createBasicDeployment(t, "sample-app", "default"),
				},
			},
		}
	}

	require.NoError(t, TakeOff(background, makeParams("foo")))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			MaydayParams:   yoke.MaydayParams{Release: "foo"},
			GlobalSettings: settings,
		}))
	}()

	require.EqualError(
		t,
		TakeOff(background, makeParams("bar")),
		`failed to apply resources: dry run: default/apps/v1/deployment/sample-app: failed to validate resource release: expected release "default/bar" but resource is already owned by "default/foo"`,
	)

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	deployment, err := client.Clientset.AppsV1().Deployments("default").Get(context.Background(), "sample-app", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(
		t,
		map[string]string{
			"app":                                      "sample-app",
			"app.kubernetes.io/managed-by":             "yoke",
			"app.kubernetes.io/yoke-release":           "foo",
			"app.kubernetes.io/yoke-release-namespace": "default",
		},
		deployment.Labels,
	)
}

func TestForceOwnership(t *testing.T) {
	makeParams := func(name string, forceOwnership bool) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:        name,
				ForceOwnership: forceOwnership,
				Flight: yoke.FlightParams{
					Input: createBasicDeployment(t, "sample-app", "default"),
				},
			},
		}
	}

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	deploymentIntf := client.Clientset.AppsV1().Deployments("default")

	var resource appsv1.Deployment
	require.NoError(t, json.NewDecoder(createBasicDeployment(t, "sample-app", "default")).Decode(&resource))

	_, err = deploymentIntf.Create(context.Background(), &resource, metav1.CreateOptions{})
	require.NoError(t, err)

	require.EqualError(
		t,
		TakeOff(background, makeParams("foo", false)),
		`failed to apply resources: dry run: default/apps/v1/deployment/sample-app: failed to validate resource release: expected release "default/foo" but resource is already owned by ""`,
	)

	require.NoError(t, TakeOff(background, makeParams("foo", true)))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{MaydayParams: yoke.MaydayParams{Release: "foo"}, GlobalSettings: settings}))
	}()

	deployment, err := deploymentIntf.Get(background, "sample-app", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "foo", deployment.GetLabels()[internal.LabelYokeRelease])
}

func TestReleaseOwnershipAcrossNamespaces(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	require.NoError(t, client.EnsureNamespace(background, "shared"))
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(background, "shared", metav1.DeleteOptions{}))
	}()

	require.NoError(t, TakeOff(background, TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:        "release",
			Namespace:      "default",
			CrossNamespace: true,
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "x", "shared"),
			},
		},
	}))

	require.ErrorContains(
		t,
		TakeOff(background, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:        "release",
				Namespace:      "shared",
				CrossNamespace: true,
				Flight: yoke.FlightParams{
					Input: createBasicDeployment(t, "x", "shared"),
				},
			},
		}),
		`failed to apply resources: dry run: shared/apps/v1/deployment/x: failed to validate resource release: expected release "shared/release" but resource is already owned by "default/release"`,
	)
}

func TestReleasesInDifferentNamespaces(t *testing.T) {
	namespaces := []string{"foo", "bar"}

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	for _, ns := range namespaces {
		require.NoError(
			t,
			TakeOff(background, TakeoffParams{
				GlobalSettings: settings,
				TakeoffParams: yoke.TakeoffParams{
					Release:         "rel",
					Namespace:       ns,
					CreateNamespace: true,
					Flight: yoke.FlightParams{
						Input: createBasicDeployment(t, "release", ""),
					},
				},
			}),
		)
		defer func() {
			require.NoError(t, Mayday(background, MaydayParams{GlobalSettings: settings, MaydayParams: yoke.MaydayParams{Release: "rel", Namespace: ns}}))
		}()

		secrets, err := client.Clientset.CoreV1().Secrets(ns).List(background, metav1.ListOptions{LabelSelector: internal.LabelKind + "=revision"})
		require.NoError(t, err)
		require.Equal(t, 1, len(secrets.Items))
	}
}

func TestTakeoffWithNamespace(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	ns := fmt.Sprintf("test-ns-%x", strconv.Itoa(rand.IntN(1024)))

	_, err = client.CoreV1().Namespaces().Get(background, ns, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:         "foo",
			Namespace:       ns,
			Flight:          yoke.FlightParams{Input: createBasicDeployment(t, "sample-app", ns)},
			CreateNamespace: true,
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{MaydayParams: yoke.MaydayParams{Release: "foo", Namespace: ns}, GlobalSettings: settings}))
		require.NoError(t, client.CoreV1().Namespaces().Delete(background, ns, metav1.DeleteOptions{}))
	}()

	_, err = client.CoreV1().Namespaces().Get(background, ns, metav1.GetOptions{})
	require.NoError(t, err)

	require.NoError(t, client.CoreV1().Namespaces().Delete(background, ns, metav1.DeleteOptions{}))
}

func TestTakeoffWithNamespaceStage(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	background := background

	ns, err := client.CoreV1().Namespaces().Get(background, "test-ns-resource", metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err) || ns.Status.Phase == corev1.NamespaceTerminating)

	params := func(withNamespaceStage bool) TakeoffParams {
		resources := func() string {
			if withNamespaceStage {
				return `[
            [
              {
                apiVersion: v1,
                kind: Namespace,
                metadata: {
                  name: test-ns-resource,
                },
              },
            ],
            [
              {
                apiVersion: v1,
                kind: ConfigMap,
                metadata: {
                  name: test-cm,
                  namespace: test-ns-resource,
                },
                data: {
                  hello: world,
                },
              },
            ],
					]`
			}
			return `{
          apiVersion: v1,
          kind: ConfigMap,
          metadata: {
            name: test-cm,
            namespace: test-ns-resource,
          },
          data: {
            hello: world,
          },
        }`
		}()

		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:        "foo",
				CrossNamespace: true,
				Flight: yoke.FlightParams{
					Input: strings.NewReader(resources),
				},
			},
		}
	}

	require.EqualError(
		t,
		TakeOff(background, params(false)),
		`failed to apply resources: dry run: test-ns-resource/core/v1/configmap/test-cm: namespaces "test-ns-resource" not found`,
	)

	require.NoError(t, TakeOff(background, params(true)))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: settings,
			MaydayParams:   yoke.MaydayParams{Release: "foo"},
		}))
		require.NoError(
			t,
			client.CoreV1().Namespaces().Delete(background, "test-ns-resource", metav1.DeleteOptions{}),
		)
	}()

	ns, err = client.CoreV1().Namespaces().Get(background, "test-ns-resource", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "foo", ns.Labels["app.kubernetes.io/yoke-release"])

	_, err = client.CoreV1().ConfigMaps("test-ns-resource").Get(background, "test-cm", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestTakeoffWithCRDResource(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	_, err = kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	params := func(withCRDStage bool) TakeoffParams {
		resources := func() string {
			if withCRDStage {
				return `[
					[
            {
              apiVersion: apiextensions.k8s.io/v1,
              kind: CustomResourceDefinition,
              metadata: {
                name: crontabs.stable.example.com,
              },
              spec: {
                group: stable.example.com,
                scope: Cluster,
                versions: [
                  {
                    name: v1,
                    served: true,
                    storage: true,
                    schema: {
                      openAPIV3Schema: {
                        type: object,
                        properties: {},
                      },
                    },
                  },
                ],
                names: {
                  plural: crontabs,
                  singular: crontab,
                  kind: CronTab,
                  shortNames: [ct],
                }
              }
            },
          ],
          [
            {
              apiVersion: stable.example.com/v1,
              kind: CronTab,
              metadata: {
                name: test,
              },
            },
          ]
				]`
			}

			return `{
        apiVersion: stable.example.com/v1,
        kind: CronTab,
        metadata: {
          name: test,
        },
      }`
		}()

		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release: "foo",
				Flight:  yoke.FlightParams{Input: strings.NewReader(resources)},
			},
		}
	}

	require.EqualError(
		t,
		TakeOff(background, params(false)),
		`setting target namespace: _/stable.example.com/v1/crontab/test: failed to lookup resource mapping: no matches for kind "CronTab" in version "stable.example.com/v1"`,
	)

	require.NoError(t, TakeOff(background, params(true)))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: settings,
			MaydayParams:   yoke.MaydayParams{Release: "foo"},
		}))
	}()
}

func TestTakeoffDiffOnly(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: strings.NewReader(`{
					apiVersion: v1,
					kind: ConfigMap,
					metadata: {
						name: test-diff,
						namespace: default,
					},
					data: {
						foo: bar,
					},
				}`),
			},
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{MaydayParams: yoke.MaydayParams{Release: "foo"}, GlobalSettings: settings}))
	}()

	var stdout bytes.Buffer
	ctx := internal.WithStdout(background, &stdout)

	params = TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:  "foo",
			DiffOnly: true,
			Flight: yoke.FlightParams{
				Input: strings.NewReader(`{
					apiVersion: v1,
					kind: ConfigMap,
					metadata: {
						name: test-diff,
						namespace: default,
					},
					data: {
						baz: boop,
					},
				}`),
			},
		},
	}

	require.NoError(t, TakeOff(ctx, params))
	require.Equal(t, "--- current\n+++ next\n@@ -4 +4 @@\n-    foo: bar\n+    baz: boop\n", stdout.String())
}

func TestDescent(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	require.EqualError(
		t,
		Descent(context.Background(), DescentParams{
			GlobalSettings: settings,
			DescentParams: yoke.DescentParams{
				Release:    "foo",
				RevisionID: 1,
			},
		}),
		`no release found "foo" in namespace "default"`,
	)

	for _, value := range []string{"a", "b"} {
		require.NoError(
			t,
			TakeOff(context.Background(), TakeoffParams{
				GlobalSettings: settings,
				TakeoffParams: yoke.TakeoffParams{
					Release: "foo",
					Flight: yoke.FlightParams{
						Input: internal.JSONReader(&corev1.ConfigMap{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "v1",
								Kind:       "ConfigMap",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name: "foo",
							},
							Data: map[string]string{"key": value},
						}),
					},
				},
			}),
		)
	}

	configMap, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "foo", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "b", configMap.Data["key"])

	require.NoError(
		t,
		Descent(context.Background(), DescentParams{
			GlobalSettings: settings,
			DescentParams: yoke.DescentParams{
				Release:    "foo",
				RevisionID: 1,
			},
		}),
	)

	configMap, err = client.CoreV1().ConfigMaps("default").Get(context.Background(), "foo", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "a", configMap.Data["key"])

	require.NoError(t, Mayday(context.Background(), MaydayParams{
		GlobalSettings: settings,
		MaydayParams:   yoke.MaydayParams{Release: "foo"},
	}))
}

func TestTurbulenceFix(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	takeoffParams := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: strings.NewReader(`{
					apiVersion: v1,
					kind: ConfigMap,
					metadata: {
						name: test,
						namespace: default,
					},
					data: {
						key: value,
					},
				}`),
			},
		},
	}

	require.NoError(t, TakeOff(background, takeoffParams))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: settings,
			MaydayParams:   yoke.MaydayParams{Release: takeoffParams.Release},
		}))
	}()

	configmap, err := client.CoreV1().ConfigMaps("default").Get(background, "test", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "value", configmap.Data["key"])

	configmap.Data["key"] = "corrupt"

	_, err = client.CoreV1().ConfigMaps("default").Update(background, configmap, metav1.UpdateOptions{})
	require.NoError(t, err)

	configmap, err = client.CoreV1().ConfigMaps("default").Get(background, "test", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "corrupt", configmap.Data["key"])

	var stdout, stderr bytes.Buffer
	ctx := internal.WithStdio(background, &stdout, &stderr, nil)

	require.NoError(
		t,
		Turbulence(ctx, TurbulenceParams{
			GlobalSettings: settings,
			TurbulenceParams: yoke.TurbulenceParams{
				Release:       "foo",
				Fix:           false,
				ConflictsOnly: true,
			},
		}),
	)

	require.Equal(
		t,
		strings.Join(
			[]string{
				"--- expected",
				"+++ actual",
				"@@ -5 +5 @@",
				"-      key: value",
				"+      key: corrupt",
				"",
			},
			"\n",
		),
		stdout.String(),
	)

	require.NoError(
		t,
		Turbulence(
			ctx,
			TurbulenceParams{
				GlobalSettings: settings,
				TurbulenceParams: yoke.TurbulenceParams{
					Release: "foo",
					Fix:     true,
				},
			},
		),
	)
	require.Equal(t, "fixed drift for: default/core/v1/configmap/test\n", stderr.String())

	configmap, err = client.CoreV1().ConfigMaps("default").Get(background, "test", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "value", configmap.Data["key"])
}

func TestLookupResource(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/flight.wasm ./internal/testing/flights/base", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	var stderr bytes.Buffer
	ctx := internal.WithStderr(background, &stderr)

	require.ErrorContains(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:   "foo",
				Namespace: "default",
				Flight:    yoke.FlightParams{Path: "./test_output/flight.wasm"},
				Wait:      10 * time.Second,
				Poll:      time.Second,
			},
		}),
		"exit_code(1)",
	)

	require.Contains(t, stderr.String(), "access to the cluster has not been granted for this flight invocation")

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:       "foo",
			Namespace:     "default",
			ClusterAccess: yoke.ClusterAccessParams{Enabled: true},
			Flight:        yoke.FlightParams{Path: "./test_output/flight.wasm"},
			Wait:          10 * time.Second,
			Poll:          time.Second,
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: params.GlobalSettings,
			MaydayParams:   yoke.MaydayParams{Release: "foo"},
		}))
	}()

	secret, err := client.Clientset.CoreV1().Secrets("default").Get(background, "foo-example", metav1.GetOptions{})
	require.NoError(t, err)

	require.NotEmpty(t, secret.Data["password"])

	err = TakeOff(background, params)
	require.NotNil(t, err)
	require.True(t, internal.IsWarning(err), "should be warning but got: %v", err)
	require.EqualError(t, err, "resources are the same as previous revision: skipping creation of new revision")

	stderr.Reset()

	require.ErrorContains(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:         "foo",
				Namespace:       "foo",
				CreateNamespace: true,
				ClusterAccess:   yoke.ClusterAccessParams{Enabled: true},
				Flight: yoke.FlightParams{
					Path:  "./test_output/flight.wasm",
					Input: strings.NewReader(`{"Namespace": "default"}`),
				},
				Wait: 10 * time.Second,
				Poll: time.Second,
			},
		}),
		"exit_code(1)",
	)

	require.Contains(t, stderr.String(), "cannot access resource outside of target release ownership")

	require.ErrorContains(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:         "foo",
				Namespace:       "foo",
				CreateNamespace: true,
				ClusterAccess:   yoke.ClusterAccessParams{Enabled: true, ResourceMatchers: []string{"default/Configmap"}},
				Flight: yoke.FlightParams{
					Path:  "./test_output/flight.wasm",
					Input: strings.NewReader(`{"Namespace": "default"}`),
				},
				Wait: 10 * time.Second,
				Poll: time.Second,
			},
		}),
		"exit_code(1)",
	)

	require.Contains(t, stderr.String(), "cannot access resource outside of target release ownership")

	require.NoError(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:         "foo",
				Namespace:       "foo",
				CreateNamespace: true,
				ClusterAccess: yoke.ClusterAccessParams{
					Enabled:          true,
					ResourceMatchers: []string{"default/*", "foo/*"},
				},
				Flight: yoke.FlightParams{
					Path:  "./test_output/flight.wasm",
					Input: strings.NewReader(`{"Namespace": "default"}`),
				},
				Wait: 10 * time.Second,
				Poll: time.Second,
			},
		}),
		"exit_code(1)",
	)
}

func TestBadVersion(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/flight.wasm ./internal/testing/flights/versioncheck", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	var stderr bytes.Buffer
	ctx := internal.WithStderr(background, &stderr)

	err := TakeOff(ctx, TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:   "foo",
			Namespace: "default",
			Flight:    yoke.FlightParams{Path: "./test_output/flight.wasm"},
			Wait:      10 * time.Second,
			Poll:      time.Second,
		},
	})
	require.ErrorContains(t, err, "exit_code(1)")
	require.Contains(t, stderr.String(), "failed to meet min version requirement for yoke")
}

func TestOciFlight(t *testing.T) {
	require.NoError(
		t,
		x.X(
			"go build -o ./test_output/basic.wasm ../../examples/basic",
			x.Env("GOOS=wasip1", "GOARCH=wasm"),
		),
	)

	require.NoError(t, x.X("docker rm -f registry"))
	require.NoError(t, x.X("docker run -d -p 5001:5000 --name registry registry:2.7"))

	require.NoError(t, yoke.Stow(context.Background(), yoke.StowParams{
		WasmFile: "./test_output/basic.wasm",
		URL:      "oci://localhost:5001/test:v1",
		Tags:     []string{"alt"},
	}))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	ctx := internal.WithStdout(context.Background(), io.Discard)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		SendToStdout: true,
		Release:      "registry",
		Flight: yoke.FlightParams{
			Path: "oci://localhost:5001/test:v1",
		},
	}))
	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "registry",
		Flight: yoke.FlightParams{
			Path: "oci://localhost:5001/test:alt",
		},
	}))
}

func TestTakeoffAssertsDesiredState(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	params := func() yoke.TakeoffParams {
		return yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "foo", ""),
			},
		}
	}

	require.NoError(t, commander.Takeoff(background, params()))

	deploymentIntf := client.Clientset.AppsV1().Deployments("default")

	dep, err := deploymentIntf.Get(background, "foo", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep)

	require.NoError(t, deploymentIntf.Delete(background, "foo", metav1.DeleteOptions{}))

	err = commander.Takeoff(background, params())
	require.True(t, internal.IsWarning(err))
	require.EqualError(t, err, "resources are the same as previous revision: skipping creation of new revision")

	dep, err = deploymentIntf.Get(background, "foo", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep)
}

func TestMayday(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	makeResources := func() []*unstructured.Unstructured {
		return []*unstructured.Unstructured{
			{
				Object: map[string]any{
					"apiVersion": "apiextensions.k8s.io/v1",
					"kind":       "CustomResourceDefinition",
					"metadata": map[string]any{
						"name": "tests.examples.com",
					},
					"spec": map[string]any{
						"group": "examples.com",
						"names": map[string]any{
							"kind":     "Test",
							"plural":   "tests",
							"singular": "test",
						},
						"scope": "Cluster",
						"versions": []any{
							map[string]any{
								"name":    "v1",
								"served":  true,
								"storage": true,
								"schema": map[string]any{
									"openAPIV3Schema": map[string]any{
										"type": "object",
									},
								},
							},
						},
					},
				},
			},
			{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Namespace",
					"metadata": map[string]any{
						"name": "foo",
					},
				},
			},
			{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "cm",
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
		}
	}

	crdIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	})

	cases := []struct {
		Name         string
		Params       yoke.MaydayParams
		Expectations func(t *testing.T)
	}{
		{
			Name:   "no removals",
			Params: yoke.MaydayParams{},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.True(t, kerrors.IsNotFound(err))

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, ns.GetLabels(), internal.LabelYokeRelease)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, crd.GetLabels(), internal.LabelYokeRelease)
			},
		},
		{
			Name: "remove crds",
			Params: yoke.MaydayParams{
				PruneOpts: yoke.PruneOpts{RemoveCRDs: true},
			},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.True(t, kerrors.IsNotFound(err))

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, ns.GetLabels(), internal.LabelYokeRelease)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, crd.GetDeletionTimestamp(), "expected a deletion timestamp on crd but got none")
				}
			},
		},
		{
			Name: "remove both",
			Params: yoke.MaydayParams{
				PruneOpts: k8s.PruneOpts{
					RemoveCRDs:       true,
					RemoveNamespaces: true,
				},
			},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.True(t, kerrors.IsNotFound(err))

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, ns.GetDeletionTimestamp(), "expected a deletion timestamp on ns but got none")
				}

				testutils.EventuallyNoErrorf(
					t,
					func() error {
						_, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
						if !kerrors.IsNotFound(err) {
							return fmt.Errorf("expected error not found but got %v", err)
						}
						return nil
					},
					time.Second,
					30*time.Second,
					"ns was not deleted",
				)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, crd.GetDeletionTimestamp(), "expected a deletion timestamp on crd but got none")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			require.NoError(
				t,
				commander.Takeoff(background, yoke.TakeoffParams{
					Release:        "test",
					ForceOwnership: true,
					Flight: yoke.FlightParams{
						Input: internal.JSONReader(makeResources()),
					},
				}),
			)
			_, err = client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
			require.NoError(t, err)

			ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
			require.NoError(t, err)
			require.Contains(t, ns.GetLabels(), internal.LabelYokeRelease)

			crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
			require.NoError(t, err)
			require.Contains(t, crd.GetLabels(), internal.LabelYokeRelease)

			tc.Params.Release = "test"

			require.NoError(t, commander.Mayday(background, tc.Params))

			tc.Expectations(t)
		})
	}
}

func TestTakeoffPruning(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	makeResources := func(with bool) []*unstructured.Unstructured {
		crd := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "apiextensions.k8s.io/v1",
				"kind":       "CustomResourceDefinition",
				"metadata": map[string]any{
					"name": "tests.examples.com",
				},
				"spec": map[string]any{
					"group": "examples.com",
					"names": map[string]any{
						"kind":     "Test",
						"plural":   "tests",
						"singular": "test",
					},
					"scope": "Cluster",
					"versions": []any{
						map[string]any{
							"name":    "v1",
							"served":  true,
							"storage": true,
							"schema": map[string]any{
								"openAPIV3Schema": map[string]any{
									"type": "object",
								},
							},
						},
					},
				},
			},
		}
		ns := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]any{
					"name": "foo",
				},
			},
		}
		results := []*unstructured.Unstructured{
			{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "cm",
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
		}

		if with {
			results = append(results, crd, ns)
		}

		return results
	}

	crdIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	})

	cases := []struct {
		Name         string
		Params       yoke.TakeoffParams
		Expectations func(t *testing.T)
	}{
		{
			Name: "no removals",
			Params: yoke.TakeoffParams{
				PruneOpts: k8s.PruneOpts{},
			},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.NoError(t, err)

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, ns.GetLabels(), internal.LabelYokeRelease)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, crd.GetLabels(), internal.LabelYokeRelease)
			},
		},
		{
			Name: "remove crds",
			Params: yoke.TakeoffParams{
				PruneOpts: yoke.PruneOpts{RemoveCRDs: true},
			},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.NoError(t, err)

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				require.NoError(t, err)
				require.NotContains(t, ns.GetLabels(), internal.LabelYokeRelease)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, crd.GetDeletionTimestamp(), "expected a deletion timestamp on crd but got none")
				}
			},
		},
		{
			Name: "remove both",
			Params: yoke.TakeoffParams{
				PruneOpts: k8s.PruneOpts{
					RemoveCRDs:       true,
					RemoveNamespaces: true,
				},
			},
			Expectations: func(t *testing.T) {
				_, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
				require.NoError(t, err)

				ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, ns.GetDeletionTimestamp(), "expected a deletion timestamp on ns but got none")
				}

				testutils.EventuallyNoErrorf(
					t,
					func() error {
						_, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
						if !kerrors.IsNotFound(err) {
							return fmt.Errorf("expected error not found but got %v", err)
						}
						return nil
					},
					time.Second,
					30*time.Second,
					"ns was not deleted",
				)

				crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
				if err != nil {
					require.True(t, kerrors.IsNotFound(err), "expected error to be not found but got: %v", err)
				} else {
					require.NotEmpty(t, crd.GetDeletionTimestamp(), "expected a deletion timestamp on crd but got none")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			require.NoError(
				t,
				commander.Takeoff(background, yoke.TakeoffParams{
					Release:        "test",
					ForceOwnership: true,
					Wait:           time.Minute,
					Flight: yoke.FlightParams{
						Input: internal.JSONReader(makeResources(true)),
					},
				}),
			)
			_, err = client.Clientset.CoreV1().ConfigMaps("default").Get(background, "cm", metav1.GetOptions{})
			require.NoError(t, err)

			ns, err := client.Clientset.CoreV1().Namespaces().Get(background, "foo", metav1.GetOptions{})
			require.NoError(t, err)
			require.Contains(t, ns.GetLabels(), internal.LabelYokeRelease)

			crd, err := crdIntf.Get(background, "tests.examples.com", metav1.GetOptions{})
			require.NoError(t, err)
			require.Contains(t, crd.GetLabels(), internal.LabelYokeRelease)

			tc.Params.Release = "test"
			tc.Params.Flight.Input = internal.JSONReader(makeResources(false))

			require.NoError(t, commander.Takeoff(background, tc.Params))

			tc.Expectations(t)
		})
	}

	require.NoError(t, commander.Mayday(background, yoke.MaydayParams{Release: "test"}))
}

func TestPruneOwnership(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	require.NoError(
		t,
		commander.Takeoff(context.Background(), yoke.TakeoffParams{
			Release: "foo",
			Flight:  yoke.FlightParams{Input: createBasicDeployment(t, "test", "")},
		}),
	)

	deploymentIntf := client.Clientset.AppsV1().Deployments("default")

	deployment, err := deploymentIntf.Get(context.Background(), "test", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "foo", deployment.Labels[internal.LabelYokeRelease])

	require.NoError(
		t,
		commander.Takeoff(context.Background(), yoke.TakeoffParams{
			Release:        "bar",
			ForceOwnership: true,
			Flight:         yoke.FlightParams{Input: createBasicDeployment(t, "test", "")},
		}),
	)

	deployment, err = deploymentIntf.Get(context.Background(), "test", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "bar", deployment.Labels[internal.LabelYokeRelease])

	require.NoError(t, commander.Mayday(context.Background(), yoke.MaydayParams{Release: "foo"}))

	deployment, err = deploymentIntf.Get(context.Background(), "test", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "bar", deployment.Labels[internal.LabelYokeRelease])

	require.NoError(t, commander.Mayday(context.Background(), yoke.MaydayParams{Release: "bar"}))

	_, err = deploymentIntf.Get(context.Background(), "test", metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))
}

func TestOptimisticLocking(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	errCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)

	_ = commander.Mayday(background, yoke.MaydayParams{Release: "foo"})

	takeoff := func(lock bool) {
		defer wg.Done()
		if err := commander.Takeoff(background, yoke.TakeoffParams{
			Release: "foo",
			Lock:    lock,
			Flight: yoke.FlightParams{
				Input: createBasicDeployment(t, "foo", ""),
			},
		}); err != nil {
			errCh <- err
		}
	}

	go takeoff(true)
	go takeoff(true)

	wg.Wait()

	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	require.Len(t, errs, 1)
	require.EqualError(t, errs[0], "failed to lock release: lock is already taken")

	errCh = make(chan error, 2)
	wg.Add(2)

	go takeoff(false)
	go takeoff(false)

	wg.Wait()

	close(errCh)

	for err := range errCh {
		require.True(t, internal.IsWarning(err), "expected error to be a warning but got: %v", err)
	}

	commander.Mayday(background, yoke.MaydayParams{Release: "foo"})
}

func TestMaxMemoryMib(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/memory.wasm ./internal/testing/flights/memory", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	err = commander.Takeoff(background, yoke.TakeoffParams{
		Release: "memory",
		Flight: yoke.FlightParams{
			Path:         "./test_output/memory.wasm",
			Args:         []string{"-mb", "2"},
			MaxMemoryMib: 10,
		},
	})
	if err != nil {
		require.True(t, internal.IsWarning(err), err.Error())
	}
	require.ErrorContains(
		t,
		commander.Takeoff(background, yoke.TakeoffParams{
			Release: "memory",
			Flight: yoke.FlightParams{
				Path:         "./test_output/memory.wasm",
				Args:         []string{"-mb", "10"},
				MaxMemoryMib: 10,
			},
		}),
		"out of memory",
	)
}

func TestTimeout(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/halt.wasm ./internal/testing/flights/halt", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	require.ErrorContains(
		t,
		yoke.FromK8Client(client).Takeoff(background, yoke.TakeoffParams{
			Release: "halt",
			Flight: yoke.FlightParams{
				Path:    "./test_output/halt.wasm",
				Timeout: 10 * time.Millisecond,
			},
		}),
		"failed to evaluate flight: failed to execute wasm: module closed with context deadline exceeded: execution timeout (10ms) exceeded",
	)
}

func TestGetRestMapping(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/restmapping.wasm ./internal/testing/flights/restmapping", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	require.ErrorContains(
		t,
		commander.Takeoff(background, yoke.TakeoffParams{
			Release: "test",
			Flight: yoke.FlightParams{
				Path:  "./test_output/restmapping.wasm",
				Input: strings.NewReader(`{ APIVersion: apps/v1, Kind: Deployment }`),
			},
			// OOPS forgot cluster access!
			ClusterAccess: yoke.ClusterAccessParams{Enabled: false},
		}),
		"failed to get rest mapping: access to the cluster has not been granted for this flight invocation",
	)

	require.NoError(
		t,
		commander.Takeoff(background, yoke.TakeoffParams{
			Release: "test",
			Flight: yoke.FlightParams{
				Path:  "./test_output/restmapping.wasm",
				Input: strings.NewReader(`{ APIVersion: apps/v1, Kind: Deployment }`),
			},
			ClusterAccess: yoke.ClusterAccessParams{Enabled: true},
		}),
	)
	defer func() {
		require.NoError(t, yoke.FromK8Client(client).Mayday(background, yoke.MaydayParams{Release: "test"}))
	}()

	cm, err := client.Clientset.CoreV1().ConfigMaps("default").Get(background, "test", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, "deployments", cm.Data["resource"])
	require.Equal(t, "true", cm.Data["namespaced"])
}

func TestReleasePrefix(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/name.wasm ./internal/testing/flights/name", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	cmIntf := client.Clientset.CoreV1().ConfigMaps("default")

	_, err = cmIntf.Get(background, "example", metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err), "expected example configmap not to be not found")

	require.NoError(t, commander.Takeoff(background, yoke.TakeoffParams{
		ReleasePrefix: "test-",
		Release:       "example",
		Flight:        yoke.FlightParams{Path: "./test_output/name.wasm"},
	}))
	defer func() {
		require.NoError(t, commander.Mayday(background, yoke.MaydayParams{Release: "test-example"}))
	}()

	_, err = cmIntf.Get(background, "example", metav1.GetOptions{})
	require.NoError(t, err)

	release, err := client.GetRelease(background, "test-example", "default")
	require.NoError(t, err)

	stages, err := client.GetRevisionResources(background, release.ActiveRevision())
	require.NoError(t, err)

	resources := stages.Flatten()

	require.Len(t, resources, 1)

	require.Equal(t, "example", resources[0].GetName())
	require.Equal(t, "ConfigMap", resources[0].GetKind())
}

func TestDefaultNamespace(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/name.wasm ./internal/testing/flights/name", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	buildCommander := func(namespace string) *yoke.Commander {
		require.NoError(t, x.Xf("kubectl config set-context --current=true --namespace=%s", []any{namespace}))
		defer func() {
			require.NoError(t, x.X("kubectl config set-context --current=true --namespace=default"))
		}()
		client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
		require.NoError(t, err)
		require.Equal(t, namespace, client.DefaultNamespace)
		return yoke.FromK8Client(client)
	}

	for _, ns := range []string{"foo", "bar", "baz"} {
		require.NoError(t, client.EnsureNamespace(t.Context(), ns))
		defer func() {
			require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(t.Context(), ns, metav1.DeleteOptions{}))
		}()

		commander := buildCommander(ns)

		require.NoError(t, commander.Takeoff(t.Context(), yoke.TakeoffParams{
			Release: "test",
			Flight: yoke.FlightParams{
				Path:  "./test_output/name.wasm",
				Input: internal.JSONReader(map[string]string{"hello": "42"}),
			},
		}))

		cm, err := client.Clientset.CoreV1().ConfigMaps(ns).Get(t.Context(), "test", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "42", cm.Data["hello"])

		require.NoError(t, commander.Takeoff(t.Context(), yoke.TakeoffParams{
			Release: "test",
			Flight: yoke.FlightParams{
				Path:  "./test_output/name.wasm",
				Input: internal.JSONReader(map[string]string{"hello": "world"}),
			},
		}))

		cm, err = client.Clientset.CoreV1().ConfigMaps(ns).Get(t.Context(), "test", metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, cm.Data, 1)
		require.Equal(t, "world", cm.Data["hello"])

		require.NoError(t, commander.Descent(t.Context(), yoke.DescentParams{Release: "test", RevisionID: 1}))

		cm, err = client.Clientset.CoreV1().ConfigMaps(ns).Get(t.Context(), "test", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "42", cm.Data["hello"])

		cm.Data["hello"] = "bye"

		_, err = client.Clientset.CoreV1().ConfigMaps(ns).Update(t.Context(), cm, metav1.UpdateOptions{})
		require.NoError(t, err)

		require.NoError(t, commander.Turbulence(t.Context(), yoke.TurbulenceParams{Release: "test", Fix: true}))

		cm, err = client.Clientset.CoreV1().ConfigMaps(ns).Get(t.Context(), "test", metav1.GetOptions{})
		require.NoError(t, err)
		if cm.Data["hello"] != "42" {
			os.Exit(1)
		}
		require.Equal(t, "42", cm.Data["hello"])

		require.NoError(t, commander.Mayday(t.Context(), yoke.MaydayParams{Release: "test"}))
	}
}
