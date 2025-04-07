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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	must(x.X("kind delete clusters --all"))
	must(x.X("kind create cluster --name=tests"))

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
	background = internal.WithStdout(context.Background(), io.Discard)
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

	client, err := k8s.NewClient(restcfg)
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
		Release:        "foo",
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
}

func TestFailApplyDryRun(t *testing.T) {
	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release: "foo",
			Flight: yoke.FlightParams{
				Input:     createBasicDeployment(t, "%invalid-chars&*", "does-not-exist"),
				Namespace: "does-not-exist",
			},
		},
	}

	require.ErrorContains(
		t,
		TakeOff(background, params),
		`failed to apply resources: dry run: does-not-exist/apps/v1/deployment/%invalid-chars&*: failed to validate resource release`,
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
		Release:        "foo",
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
		require.NoError(t, Mayday(background, MaydayParams{Release: "foo", GlobalSettings: settings}))
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
			CrossNamespace: true,
			Flight: yoke.FlightParams{
				Namespace: "default",
				Input:     createBasicDeployment(t, "x", "shared"),
			},
		},
	}))

	require.ErrorContains(
		t,
		TakeOff(background, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:        "release",
				CrossNamespace: true,
				Flight: yoke.FlightParams{
					Namespace: "shared",
					Input:     createBasicDeployment(t, "x", "shared"),
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
					CreateNamespace: true,
					Flight: yoke.FlightParams{
						Input:     createBasicDeployment(t, "release", ""),
						Namespace: ns,
					},
				},
			}),
		)
		defer func() {
			require.NoError(t, Mayday(background, MaydayParams{GlobalSettings: settings, Release: "rel", Namespace: ns}))
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
			Release: "foo",
			Flight: yoke.FlightParams{
				Input:     createBasicDeployment(t, "sample-app", ns),
				Namespace: ns,
			},
			CreateNamespace: true,
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{Release: "foo", Namespace: ns, GlobalSettings: settings}))
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
			Release:        "foo",
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
						Input: testutils.JsonReader(&corev1.ConfigMap{
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
		Release:        "foo",
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
	require.NoError(t, x.X("go build -o ./test_output/flight.wasm ./internal/testing/flight", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	var stderr bytes.Buffer
	ctx := internal.WithStderr(background, &stderr)

	require.ErrorContains(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release: "foo",
				Flight: yoke.FlightParams{
					Path:      "./test_output/flight.wasm",
					Namespace: "default",
				},
				Wait: 10 * time.Second,
				Poll: time.Second,
			},
		}),
		"exit_code(1)",
	)

	require.Contains(t, stderr.String(), "access to the cluster has not been granted for this flight invocation")

	params := TakeoffParams{
		GlobalSettings: settings,
		TakeoffParams: yoke.TakeoffParams{
			Release:       "foo",
			ClusterAccess: true,
			Flight: yoke.FlightParams{
				Path:      "./test_output/flight.wasm",
				Namespace: "default",
			},
			Wait: 10 * time.Second,
			Poll: time.Second,
		},
	}

	require.NoError(t, TakeOff(background, params))
	defer func() {
		require.NoError(t, Mayday(background, MaydayParams{
			GlobalSettings: params.GlobalSettings,
			Release:        "foo",
		}))
	}()

	secret, err := client.Clientset.CoreV1().Secrets("default").Get(background, "foo-example", metav1.GetOptions{})
	require.NoError(t, err)

	require.NotEmpty(t, secret.Data["password"])

	err = TakeOff(background, params)
	require.NotNil(t, err)
	require.True(t, internal.IsWarning(err), "should be warning but got: %v", err)
	require.EqualError(t, err, "resources are the same as previous revision: skipping takeoff")

	stderr.Reset()

	require.ErrorContains(
		t,
		TakeOff(ctx, TakeoffParams{
			GlobalSettings: settings,
			TakeoffParams: yoke.TakeoffParams{
				Release:         "foo",
				CreateNamespace: true,
				ClusterAccess:   true,
				Flight: yoke.FlightParams{
					Path:      "./test_output/flight.wasm",
					Namespace: "foo",
					Input:     strings.NewReader(`{"Namespace": "default"}`),
				},
				Wait: 10 * time.Second,
				Poll: time.Second,
			},
		}),
		"exit_code(1)",
	)

	require.Contains(t, stderr.String(), "cannot access resource outside of target release ownership")
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
