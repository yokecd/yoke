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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1config "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1config "k8s.io/client-go/applyconfigurations/core/v1"
	metav1config "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestMain(m *testing.M) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(x.X("kind delete clusters --all"))
	must(x.X("kind create cluster --name=tests"))

	os.Exit(m.Run())
}

var background = context.Background()

func createBasicDeployment(t *testing.T, name, namespace string) io.Reader {
	deployment := appsv1config.Deployment(name, namespace).
		WithLabels(map[string]string{"app": name}).
		WithSpec(
			appsv1config.DeploymentSpec().
				WithSelector(metav1config.LabelSelector().
					WithMatchLabels(map[string]string{"app": name}),
				).
				WithTemplate(
					corev1config.PodTemplateSpec().
						WithLabels(map[string]string{"app": name}).
						WithSpec(
							corev1config.PodSpec().WithContainers(
								corev1config.Container().
									WithName(name).
									WithImage("alpine:latest").
									WithCommand("watch", "echo", "hello", "world"),
							)),
				),
		)

	data, err := json.Marshal(deployment)
	require.NoError(t, err)

	return bytes.NewReader(data)
}

func TestCreateDeleteCycle(t *testing.T) {
	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}
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

	client, err := k8s.NewClient(restcfg)
	require.NoError(t, err)

	revisions, err := client.GetAllRevisions(background)
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
		Release:        "foo",
	}))

	deployments, err = defaultDeployments.List(background, metav1.ListOptions{})
	require.NoError(t, err)

	require.Len(t, deployments.Items, 0)
}

func TestCreateWithWait(t *testing.T) {
	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}
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
			Release:        "foo",
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

	require.NoError(t, mayday())
}

func TestFailApplyDryRun(t *testing.T) {
	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input:     createBasicDeployment(t, "sample-app", "does-not-exist"),
				Namespace: "does-not-exist",
			},
		},
	}

	require.EqualError(
		t,
		TakeOff(background, params),
		`failed to apply resources: dry run: does-not-exist/apps/v1/deployment/sample-app: namespaces "does-not-exist" not found`,
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

	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

	makeParams := func(multiNamespace bool) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:         "foo",
				MultiNamespaces: multiNamespace,
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
		Release:        "foo",
		GlobalSettings: settings,
	}))
}

func TestReleaseOwnership(t *testing.T) {
	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

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
		require.NoError(t, Mayday(background, MaydayParams{Release: "foo", GlobalSettings: settings}))
	}()

	require.EqualError(
		t,
		TakeOff(background, makeParams("bar")),
		`failed to apply resources: dry run: default/apps/v1/deployment/sample-app: failed to validate resource release: expected release "bar" but resource is already owned by "foo"`,
	)

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	deployment, err := client.Clientset.AppsV1().Deployments("default").Get(context.Background(), "sample-app", metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(
		t,
		map[string]string{
			"app":                            "sample-app",
			"app.kubernetes.io/managed-by":   "yoke",
			"app.kubernetes.io/yoke-release": "foo",
		},
		deployment.Labels,
	)
}

func TestTakeoffWithNamespace(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	namespaceName := fmt.Sprintf("test-ns-%x", strconv.Itoa(rand.IntN(1024)))

	_, err = client.CoreV1().Namespaces().Get(background, namespaceName, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))

	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input:     createBasicDeployment(t, "sample-app", namespaceName),
				Namespace: namespaceName,
			},
			CreateNamespaces: true,
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{Release: "foo", GlobalSettings: settings}))
		require.NoError(t, client.CoreV1().Namespaces().Delete(background, namespaceName, metav1.DeleteOptions{}))
	}()

	_, err = client.CoreV1().Namespaces().Get(background, namespaceName, metav1.GetOptions{})
	require.NoError(t, err)

	require.NoError(t, client.CoreV1().Namespaces().Delete(background, namespaceName, metav1.DeleteOptions{}))
}

func TestTakeoffWithNamespaceResource(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	background := background

	ns, err := client.CoreV1().Namespaces().Get(background, "test-ns-resource", metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err) || ns.Status.Phase == corev1.NamespaceTerminating)

	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

	params := func(createNamespaces bool) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:          "foo",
				CreateNamespaces: createNamespaces,
				Flight: yoke.FlightParams{
					Namespace: "test-ns-resource",
					Input: strings.NewReader(`[
						{
							apiVersion: v1,
							kind: Namespace,
							metadata: {
								name: test-ns-resource,
							},
						},
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
					]`),
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
			Release:        "foo",
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

	background := background

	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

	params := func(createCRDs bool) TakeoffParams {
		return TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:    "foo",
				CreateCRDs: createCRDs,
				Flight: yoke.FlightParams{
					Input: strings.NewReader(`[
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
					{
						apiVersion: stable.example.com/v1,
						kind: CronTab,
						metadata: {
							name: test,
						},
					},
				]`),
				},
			},
		}
	}

	require.EqualError(
		t,
		TakeOff(background, params(false)),
		`failed to apply resources: dry run: _/stable.example.com/v1/crontab/test: failed to resolve resource: no matches for kind "CronTab" in version "stable.example.com/v1"`,
	)

	require.NoError(t, TakeOff(background, params(true)))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: settings,
			Release:        "foo",
		}))
	}()
}

func TestTakeoffDiffOnly(t *testing.T) {
	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

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
		require.NoError(t, Mayday(background, MaydayParams{Release: "foo", GlobalSettings: settings}))
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

func TestTurbulenceFix(t *testing.T) {
	rest, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	client, err := kubernetes.NewForConfig(rest)
	require.NoError(t, err)

	settings := GlobalSettings{KubeConfigPath: home.Kubeconfig}

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
			Release:        takeoffParams.Release,
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

	require.NoError(t, Turbulence(ctx, TurbulenceParams{GlobalSettings: settings, Release: "foo", Fix: false, ConflictsOnly: true}))
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

	require.NoError(t, Turbulence(ctx, TurbulenceParams{GlobalSettings: settings, Release: "foo", Fix: true}))
	require.Equal(t, "fixed drift for: default/core/v1/configmap/test\n", stderr.String())

	configmap, err = client.CoreV1().ConfigMaps("default").Get(background, "test", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "value", configmap.Data["key"])
}
