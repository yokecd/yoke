package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/cmd/atc-installer/installer"
	backendv1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	backendv2 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v2"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

type EmptyCRD struct {
	metav1.TypeMeta
	metav1.ObjectMeta `json:"metadata"`
}

func TestMain(m *testing.M) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(os.RemoveAll("./test_output"))
	must(os.MkdirAll("./test_output", 0o755))

	must(x.X("kind delete cluster --name=atc-test"))

	must(x.X("kind create cluster --name=atc-test --config -", x.Input(strings.NewReader(`
    kind: Cluster
    apiVersion: kind.x-k8s.io/v1alpha4
    nodes:
    - role: control-plane
      extraPortMappings:
      - containerPort: 30000
        hostPort: 80
        listenAddress: "127.0.0.1"
        protocol: TCP
    `))))

	if ci, _ := strconv.ParseBool(os.Getenv("CI")); !ci {
		must(x.X("kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml"))
		must(x.X(`kubectl patch -n kube-system deployment metrics-server --type=json -p [{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]`))
	}

	must(x.X(
		"go build -o ./test_output/atc-installer.wasm ../atc-installer",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))
	must(x.X(
		"go build -o ./test_output/backend.v1.wasm ./internal/testing/apis/backend/v1/flight",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))

	must(x.X(
		"docker build -t yokecd/atc:test -f Dockerfile.atc .",
		x.Dir("../.."),
	))
	must(x.X("kind load --name=atc-test docker-image yokecd/atc:test"))

	must(x.X("docker build -t yokecd/wasmcache:test -f ./internal/testing/Dockerfile.wasmcache ../.."))
	must(x.X("kind load --name=atc-test docker-image yokecd/wasmcache:test"))

	must(x.X("docker build -t yokecd/c4ts:test -f ./internal/testing/Dockerfile.c4ts ./internal/testing"))
	must(x.X("kind load --name=atc-test docker-image yokecd/c4ts:test"))

	client := internal.Must2(k8s.NewClientFromKubeConfig(home.Kubeconfig))
	commander := yoke.FromK8Client(client)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	must(commander.Takeoff(ctx, yoke.TakeoffParams{
		Release:   "atc",
		Namespace: "atc",
		Flight: yoke.FlightParams{
			Path: "./test_output/atc-installer.wasm",
			Input: internal.JSONReader(installer.Config{
				Image:           "yokecd/atc",
				Version:         "test",
				LogFormat:       "text",
				ModuleAllowList: []string{"http://wasmcache/*"},
			}),
			Args: []string{"--skip-version-check"},
		},
		CreateNamespace: true,
		Wait:            120 * time.Second,
		Poll:            time.Second,
	}))

	must(commander.Takeoff(ctx, yoke.TakeoffParams{
		Release:   "wasmcache",
		Namespace: "atc",
		Flight: yoke.FlightParams{
			Path: "./test_output/backend.v1.wasm",
			Input: internal.JSONReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "wasmcache",
				},
				Spec: backendv1.BackendSpec{
					Image:       "yokecd/wasmcache:test",
					Replicas:    1,
					HealthCheck: "/health",
				},
			}),
		},
		CreateNamespace: true,
		Wait:            30 * time.Second,
		Poll:            time.Second,
	}))

	os.Exit(m.Run())
}

func TestAirTrafficController(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), func(value bool) *bool { return &value }(true))

	commander := yoke.FromK8Client(client)

	require.ErrorContains(
		t,
		commander.Takeoff(ctx, yoke.TakeoffParams{
			Release: "backend-airway",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(v1alpha1.Airway{
					ObjectMeta: metav1.ObjectMeta{
						Name: "backends.examples.com",
					},
					Spec: v1alpha1.AirwaySpec{
						WasmURLs: v1alpha1.WasmURLs{
							Flight: "http://wasmcache/flight.v1.wasm",
						},
						Template: apiextv1.CustomResourceDefinitionSpec{
							Group: "examples.com",
							Names: apiextv1.CustomResourceDefinitionNames{
								Plural:     "backends",
								Singular:   "backend",
								ShortNames: []string{"be"},
								Kind:       "Backend",
							},
							Scope: apiextv1.NamespaceScoped,
							Versions: []apiextv1.CustomResourceDefinitionVersion{
								{
									Name:    "v1",
									Served:  true,
									Storage: false, // THIS SHOULD TRIGGER AN ERROR. Invalid to have no storage version. This should be caught by admission validation.
									Schema: &apiextv1.CustomResourceValidation{
										OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
									},
								},
							},
						},
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}),
		`admission webhook "airways.yoke.cd" denied the request`,
	)

	airwayTakeoffParams := yoke.TakeoffParams{
		Release: "backend-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/flight.v1.wasm",
					},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:     "backends",
							Singular:   "backend",
							ShortNames: []string{"be"},
							Kind:       "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}

	require.NoError(t, commander.Takeoff(ctx, airwayTakeoffParams))

	airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "yoke.cd",
		Version:  "v1alpha1",
		Resource: "airways",
	})

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			resource, err := airwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if !k8s.FlightIsReady(resource) {
				return fmt.Errorf("expected airway to be Ready but was not.")
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"airway never became ready",
	)

	takeoffErr := commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "c4ts",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "c4ts",
				},
				Spec: backendv1.BackendSpec{
					Image:    "yokecd/c4ts:test",
					Replicas: -5,
					Labels:   map[string]string{"invalid-label": "!@#$%^&*()"},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	})

	require.ErrorContains(t, takeoffErr, `admission webhook "backends.examples.com" denied the request`)
	require.ErrorContains(t, takeoffErr, `metadata.labels: Invalid value`)
	require.ErrorContains(t, takeoffErr, `spec.replicas: Invalid value`)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "c4ts",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "c4ts",
				},
				Spec: backendv1.BackendSpec{
					Image:    "yokecd/c4ts:test",
					Replicas: 3,
					Labels:   map[string]string{"test.app": "c4ts"},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))

	listCatOpts := metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
			MatchLabels: map[string]string{"test.app": "c4ts"},
		}),
	}

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			pods, err := client.Clientset.CoreV1().Pods("default").List(ctx, listCatOpts)
			if err != nil {
				return err
			}

			if expected, actual := 3, len(pods.Items); expected != actual {
				return fmt.Errorf("expected %d replicas but got %d", expected, actual)
			}

			return nil
		},
		time.Second,
		30*time.Second,
		"failed to assert expected replica count for c4ts backend deployment",
	)

	require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "c4ts"}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			pods, err := client.Clientset.CoreV1().Pods("default").List(ctx, listCatOpts)
			if err != nil {
				return err
			}
			if count := len(pods.Items); count != 0 {
				return fmt.Errorf("expected no pods but found: %d", count)
			}
			return nil
		},
		time.Second,
		2*time.Minute,
		"c4ts assets are not cleaned up after delete",
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			// In CI validation webhook get connection refused...
			// TODO: investigate if we can avoid this without retry logic.
			return commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "backend-airway",
				Flight: yoke.FlightParams{
					Input: internal.JSONReader(v1alpha1.Airway{
						TypeMeta: metav1.TypeMeta{},
						ObjectMeta: metav1.ObjectMeta{
							Name: "backends.examples.com",
						},
						Spec: v1alpha1.AirwaySpec{
							WasmURLs: v1alpha1.WasmURLs{
								Flight:    "http://wasmcache/flight.v2.wasm",
								Converter: "http://wasmcache/converter.wasm",
							},
							Template: apiextv1.CustomResourceDefinitionSpec{
								Group: "examples.com",
								Names: apiextv1.CustomResourceDefinitionNames{
									Plural:     "backends",
									Singular:   "backend",
									ShortNames: []string{"be"},
									Kind:       "Backend",
								},
								Scope: apiextv1.NamespaceScoped,
								Versions: []apiextv1.CustomResourceDefinitionVersion{
									{
										Name:    "v1",
										Served:  true,
										Storage: false,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
										},
									},
									{
										Name:    "v2",
										Served:  true,
										Storage: true,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFor[backendv2.Backend](),
										},
									},
								},
							},
						},
					}),
				},
				Wait: 30 * time.Second,
				Poll: time.Second,
			})
		},
		time.Second,
		10*time.Second,
		"failed to update airway",
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			resources, err := client.Clientset.ServerResourcesForGroupVersion("examples.com/v2")
			if err != nil {
				return err
			}

			if _, found := internal.Find(resources.APIResources, func(value metav1.APIResource) bool {
				return value.Kind == "Backend"
			}); !found {
				return fmt.Errorf("no Backend V2 found")
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to detect new Backend version",
	)

	// Although we create a v1 version we will be able to fetch it as a v2 version.
	require.NoError(
		t,
		commander.Takeoff(ctx, yoke.TakeoffParams{
			Release: "c4ts",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(backendv1.Backend{
					ObjectMeta: metav1.ObjectMeta{Name: "c4ts"},
					Spec: backendv1.BackendSpec{
						Image:    "yokecd/c4ts:test",
						Replicas: 1,
						Labels:   map[string]string{"test.app": "c4ts"},
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}),
	)

	getC4ts := func() backendv2.Backend {
		rawBackend, err := client.Dynamic.
			Resource(schema.GroupVersionResource{Group: "examples.com", Version: "v2", Resource: "backends"}).
			Namespace("default").
			Get(context.Background(), "c4ts", metav1.GetOptions{})

		require.NoError(t, err)

		var bv2 backendv2.Backend
		require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(rawBackend.Object, &bv2))

		return bv2
	}

	require.Equal(
		t,
		backendv2.BackendSpec{
			Img:      "yokecd/c4ts:test",
			Replicas: 1,
			Meta: backendv2.Meta{
				Labels:      map[string]string{"test.app": "c4ts"},
				Annotations: nil,
			},
		},
		getC4ts().Spec,
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployments, err := client.Clientset.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
			if count := len(deployments.Items); count != 1 {
				return fmt.Errorf("expected 1 deployment but got %d", count)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to view backend deployment",
	)

	if setupOnly, _ := strconv.ParseBool(os.Getenv("SETUP_ONLY")); setupOnly {
		return
	}

	// Validations must be performed against the flight override module. The backend/v2/dev module fails if replicas is even.
	require.ErrorContains(
		t,
		commander.Takeoff(ctx, yoke.TakeoffParams{
			Release: "c4ts",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(backendv2.Backend{
					ObjectMeta: metav1.ObjectMeta{
						Name: "c4ts",
						Annotations: map[string]string{
							flight.AnnotationOverrideFlight: "http://wasmcache/flight.dev.wasm",
						},
					},
					Spec: backendv2.BackendSpec{
						Img:      "yokecd/c4ts:test",
						Replicas: 2,
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}),
		"replicas must be odd but got 2",
	)

	require.NoError(
		t,
		commander.Takeoff(ctx, yoke.TakeoffParams{
			Release: "c4ts",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(backendv2.Backend{
					ObjectMeta: metav1.ObjectMeta{
						Name: "c4ts",
						Annotations: map[string]string{
							flight.AnnotationOverrideFlight: "http://wasmcache/flight.dev.wasm",
						},
					},
					Spec: backendv2.BackendSpec{
						Img:      "yokecd/c4ts:test",
						Replicas: 1,
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}),
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployments, err := client.Clientset.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("failed to list deployments: %w", err)
			}
			if count := len(deployments.Items); count != 0 {
				return fmt.Errorf("expected no deployments but got: %d", count)
			}
			daemonsets, err := client.Clientset.AppsV1().DaemonSets("default").List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("failed to list deployments: %w", err)
			}
			if count := len(daemonsets.Items); count != 1 {
				return fmt.Errorf("expected 1 daemonsets but got: %d", count)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to see dev wasm take over",
	)

	airway, err := airwayIntf.Get(context.Background(), "backends.examples.com", metav1.GetOptions{})
	require.NoError(t, err)

	var typedAirway v1alpha1.Airway
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(airway.Object, &typedAirway))

	require.EqualValues(t, []string{"yoke.cd/strip.airway"}, typedAirway.Finalizers)

	require.NoError(t, airwayIntf.Delete(context.Background(), typedAirway.Name, metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			value, err := airwayIntf.Get(context.Background(), airway.GetName(), metav1.GetOptions{})
			if value != nil {
				return fmt.Errorf("expected airway resource to be deleted but is non nil")
			}
			if !kerrors.IsNotFound(err) {
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to delete the backend airway",
	)

	validationWebhookIntf := client.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations()

	_, err = validationWebhookIntf.Get(ctx, typedAirway.CRGroupResource().String(), metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))

	crdIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    apiextv1.SchemeGroupVersion.Group,
		Version:  apiextv1.SchemeGroupVersion.Version,
		Resource: "customresourcedefinitions",
	})

	_, err = crdIntf.Get(ctx, typedAirway.Name, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))
}

func TestRestarts(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "backend-airway",
				Flight: yoke.FlightParams{
					Input: internal.JSONReader(v1alpha1.Airway{
						ObjectMeta: metav1.ObjectMeta{
							Name: "backends.examples.com",
						},
						Spec: v1alpha1.AirwaySpec{
							WasmURLs: v1alpha1.WasmURLs{
								Flight: "http://wasmcache/flight.v1.wasm",
							},
							Template: apiextv1.CustomResourceDefinitionSpec{
								Group: "examples.com",
								Names: apiextv1.CustomResourceDefinitionNames{
									Plural:     "backends",
									Singular:   "backend",
									ShortNames: []string{"be"},
									Kind:       "Backend",
								},
								Scope: apiextv1.NamespaceScoped,
								Versions: []apiextv1.CustomResourceDefinitionVersion{
									{
										Name:    "v1",
										Served:  true,
										Storage: true,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
										},
									},
								},
							},
						},
					}),
				},
				Wait: 30 * time.Second,
				Poll: time.Second,
			}); err != nil {
				if internal.IsWarning(err) {
					fmt.Println("WARNING:", err)
					return nil
				}
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create airway",
	)

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "backend-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.Dynamic.
					Resource(schema.GroupVersionResource{Group: "yoke.cd", Version: "v1alpha1", Resource: "airways"}).
					Get(ctx, "backends.examples.com", metav1.GetOptions{})

				if kerrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("expected airway not found but got no error")
			},
			time.Second,
			30*time.Second,
			"airway was never fully removed",
		)
	}()

	validationWebhookIntf := client.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations()

	expectedValidationWebhooks := []string{"atc-airway", "atc-flight", "atc-resources", "atc-external-resources"}
	for _, webhook := range expectedValidationWebhooks {
		_, err := validationWebhookIntf.Get(ctx, webhook, metav1.GetOptions{})
		require.NoError(t, err)
	}

	for _, scale := range []int32{0, 1} {
		atc, err := client.Clientset.AppsV1().Deployments("atc").Get(ctx, "atc-atc", metav1.GetOptions{})
		require.NoError(t, err)

		atc.Spec.Replicas = ptr.To(scale)

		_, err = client.Clientset.AppsV1().Deployments("atc").Update(ctx, atc, metav1.UpdateOptions{FieldManager: "yoke"})
		require.NoError(t, err)

		start := time.Now()
		testutils.EventuallyNoErrorf(
			t,
			func() error {
				atc, err := client.Clientset.AppsV1().Deployments("atc").Get(ctx, atc.Name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get atc deployment: %w", err)
				}
				if replicas := atc.Spec.Replicas; replicas == nil || *replicas != scale {
					return fmt.Errorf("unexpected replicas on atc deployment: wanted %d got %v", scale, replicas)
				}
				pods, err := client.Clientset.CoreV1().Pods("atc").List(ctx, metav1.ListOptions{
					LabelSelector: "yoke.cd/app=atc",
				})
				if err != nil {
					return err
				}
				for _, pod := range pods.Items {
					t.Log(pod.Name, pod.DeletionTimestamp)
				}
				if count := len(pods.Items); count != int(scale) {
					return fmt.Errorf("expected %d pods but got %d", scale, count)
				}
				t.Log("Scale Event Timer:", scale, time.Since(start).String())

				for _, webhook := range expectedValidationWebhooks {
					_, err := validationWebhookIntf.Get(ctx, webhook, metav1.GetOptions{})
					if scale == 0 && !kerrors.IsNotFound(err) {
						return fmt.Errorf("expected webhook %q to be not found but got: %v", webhook, err)
					} else if scale == 1 && err != nil {
						return fmt.Errorf("expected webhook %q to be present but got error: %v", webhook, err)
					}
				}

				return nil
			},
			time.Second/2,
			time.Minute,
			"pods did not scale to 0",
		)
	}

	// Connection issue when attempting webhook. Therefore wrap in an eventually.
	// TODO: investigate webhook readiness
	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release:   "example",
				Namespace: "default",
				Flight: yoke.FlightParams{
					Input: internal.JSONReader(backendv1.Backend{
						ObjectMeta: metav1.ObjectMeta{
							Name: "example",
						},
						Spec: backendv1.BackendSpec{
							Image:    "nginx:latest",
							Replicas: 1,
						},
					}),
				},
				Wait: 30 * time.Second,
				Poll: time.Second,
			}); err != nil && !internal.IsWarning(err) {
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create backend",
	)
}

func TestCrossNamespace(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	airwayWithCrossNamespace := func(crossNamespace bool) v1alpha1.Airway {
		return v1alpha1.Airway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tests.examples.com",
			},
			Spec: v1alpha1.AirwaySpec{
				WasmURLs: v1alpha1.WasmURLs{
					Flight: "http://wasmcache/crossnamespace.wasm",
				},
				Template: apiextv1.CustomResourceDefinitionSpec{
					Group: "examples.com",
					Names: apiextv1.CustomResourceDefinitionNames{
						Plural:   "tests",
						Singular: "test",
						Kind:     "Test",
					},
					Scope: func() apiextv1.ResourceScope {
						if crossNamespace {
							return apiextv1.ClusterScoped
						}
						return apiextv1.NamespaceScoped
					}(),
					Versions: []apiextv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1",
							Served:  true,
							Storage: true,
							Schema: &apiextv1.CustomResourceValidation{
								OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
							},
						},
					},
				},
			},
		}
	}

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "crossnamespace-airway",
				Flight:  yoke.FlightParams{Input: internal.JSONReader(airwayWithCrossNamespace(false))},
				Wait:    5 * time.Second,
				Poll:    time.Second,
			}); err != nil && !internal.IsWarning(err) {
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create airway",
	)

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "crossnamespace-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := airwayIntf.Get(ctx, "tests.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("tests.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to cleanup crossnamepace test resources",
		)
	}()

	for _, ns := range []string{"foo", "bar"} {
		require.NoError(t, client.EnsureNamespace(context.Background(), ns))
	}

	testIntfCluster := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "tests",
	})

	testIntfNS := testIntfCluster.Namespace("default")

	emptyTest := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "examples.com/v1",
			"kind":       "Test",
			"metadata": map[string]any{
				"name": "test",
			},
		},
	}

	_, err = testIntfNS.Create(ctx, emptyTest, metav1.CreateOptions{})
	require.ErrorContains(t, err, "Multiple namespaces detected (if desired enable multinamespace releases)")

	require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "crossnamespace-airway"}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "crossnamespace-airway",
				Flight:  yoke.FlightParams{Input: internal.JSONReader(airwayWithCrossNamespace(true))},
				Wait:    30 * time.Second,
				Poll:    time.Second,
			}); err != nil && !internal.IsWarning(err) {
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create airway",
	)

	_, err = testIntfCluster.Create(ctx, emptyTest, metav1.CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, client.AirwayIntf.Delete(context.Background(), "tests.examples.com", metav1.DeleteOptions{}))
}

func TestClusterScopeDynamicAirway(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	airway := v1alpha1.Airway{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tests.examples.com",
		},
		Spec: v1alpha1.AirwaySpec{
			WasmURLs: v1alpha1.WasmURLs{
				Flight: "http://wasmcache/crossnamespace.wasm",
			},
			Mode: v1alpha1.AirwayModeDynamic,
			Template: apiextv1.CustomResourceDefinitionSpec{
				Group: "examples.com",
				Names: apiextv1.CustomResourceDefinitionNames{
					Plural:   "tests",
					Singular: "test",
					Kind:     "Test",
				},
				Scope: apiextv1.ClusterScoped,
				Versions: []apiextv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextv1.CustomResourceValidation{
							OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
						},
					},
				},
			},
		},
	}

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "cluster-airway",
		Flight:  yoke.FlightParams{Input: internal.JSONReader(airway)},
		Wait:    30 * time.Second,
		Poll:    time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "cluster-airway"}))
		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if _, err := client.AirwayIntf.Get(ctx, "tests.examples.com", metav1.GetOptions{}); err == nil {
					return fmt.Errorf("tests.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to cleanup cluster test resources",
		)
	}()

	for _, ns := range []string{"foo", "bar"} {
		require.NoError(t, client.EnsureNamespace(context.Background(), ns))
	}

	testIntf := k8s.TypedInterface[EmptyCRD](client.Dynamic, schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "tests",
	})

	emptyTest := EmptyCRD{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "examples.com/v1",
			Kind:       "Test",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	_, err = testIntf.Create(ctx, &emptyTest, metav1.CreateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			for _, ns := range []string{"foo", "bar"} {
				cm, err := client.Clientset.CoreV1().ConfigMaps(ns).Get(context.Background(), "cm", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to find expected configmap in namespace %s: %w", ns, err)
				}
				if !cm.GetDeletionTimestamp().IsZero() {
					return fmt.Errorf("unexpected configmap state: %s/%s: has deletion timestamp", ns, cm.Name)
				}
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create subresources",
	)

	for _, ns := range []string{"foo", "bar"} {
		if err := client.Clientset.CoreV1().ConfigMaps(ns).Delete(context.Background(), "cm", metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
			require.NoError(t, err)
		}
	}

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			for _, ns := range []string{"foo", "bar"} {
				cm, err := client.Clientset.CoreV1().ConfigMaps(ns).Get(context.Background(), "cm", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to find expected configmap in namespace %s: %w", ns, err)
				}
				if cm.DeletionTimestamp != nil {
					return fmt.Errorf("cm in namespace %s has deletion timestamp and expected none", ns)
				}
			}
			return nil
		},
		time.Second,
		10*time.Second,
		"failed to create subresources",
	)
}

func TestHistoryCap(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), func(value bool) *bool { return &value }(true))

	commander := yoke.FromK8Client(client)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			// In CI validation webhook get connection refused...
			// TODO: investigate if we can avoid this without retry logic.
			return commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "backend-airway",
				Flight: yoke.FlightParams{
					Input: internal.JSONReader(v1alpha1.Airway{
						TypeMeta: metav1.TypeMeta{},
						ObjectMeta: metav1.ObjectMeta{
							Name: "backends.examples.com",
						},
						Spec: v1alpha1.AirwaySpec{
							WasmURLs: v1alpha1.WasmURLs{
								Flight: "http://wasmcache/flight.v1.wasm",
							},
							Template: apiextv1.CustomResourceDefinitionSpec{
								Group: "examples.com",
								Names: apiextv1.CustomResourceDefinitionNames{
									Plural:     "backends",
									Singular:   "backend",
									ShortNames: []string{"be"},
									Kind:       "Backend",
								},
								Scope: apiextv1.NamespaceScoped,
								Versions: []apiextv1.CustomResourceDefinitionVersion{
									{
										Name:    "v1",
										Served:  true,
										Storage: true,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
										},
									},
								},
							},
						},
					}),
				},
				Wait: 30 * time.Second,
				Poll: time.Second,
			})
		},
		time.Second,
		10*time.Second,
		"failed to create airway",
	)
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "backend-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := airwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("tests.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to cleanup crossnamepace test resources",
		)
	}()

	backendIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "backends",
		}).
		Namespace("default")

	backend, err := internal.ToUnstructured(&backendv1.Backend{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: backendv1.BackendSpec{
			Image:    "yokecd/c4ts:test",
			Replicas: 1,
			Labels:   map[string]string{},
		},
	})
	require.NoError(t, err)

	backend, err = backendIntf.Create(ctx, backend, metav1.CreateOptions{})
	require.NoError(t, err)

	for i := range 3 {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			backend, err = backendIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			unstructured.SetNestedMap(backend.Object, map[string]any{"test": strconv.Itoa(i + 1)}, "spec", "labels")
			backend, err = backendIntf.Update(ctx, backend, metav1.UpdateOptions{})
			return err
		})
		require.NoError(t, err)

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				deployment, err := client.Clientset.AppsV1().Deployments("default").Get(ctx, "test", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get deployment: %w", err)
				}
				if len(deployment.Labels) == 0 {
					return fmt.Errorf("deployment does not have labels")
				}
				if label := deployment.Labels["test"]; label != strconv.Itoa(i+1) {
					return fmt.Errorf("did not see updated label got %q but expected %q", label, strconv.Itoa(i+1))
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"expected to see updated label",
		)

	}

	release, err := client.GetRelease(ctx, atc.ReleaseName(backend), "default")
	require.NoError(t, err)
	require.Len(t, release.History, 2)

	for _, revision := range release.History {
		require.Equal(t, "http://wasmcache/flight.v1.wasm", revision.Source.Ref)
		require.NotEmpty(t, revision.Source.Checksum)
	}

	slices.SortStableFunc(release.History, func(a, b internal.Revision) int {
		return b.ActiveAt.Compare(a.ActiveAt)
	})

	for i, label := range []string{"3", "2"} {
		resources, err := client.GetRevisionResources(ctx, release.History[i])
		require.NoError(t, err)

		dep, _ := internal.Find(resources.Flatten(), func(elem *unstructured.Unstructured) bool { return elem.GetKind() == "Deployment" })
		require.Equal(t, label, dep.GetLabels()["test"])
	}

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			// In CI validation webhook get connection refused...
			// TODO: investigate if we can avoid this without retry logic.
			return commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "backend-airway",
				Flight: yoke.FlightParams{
					Input: internal.JSONReader(v1alpha1.Airway{
						TypeMeta: metav1.TypeMeta{},
						ObjectMeta: metav1.ObjectMeta{
							Name: "backends.examples.com",
						},
						Spec: v1alpha1.AirwaySpec{
							WasmURLs: v1alpha1.WasmURLs{
								Flight: "http://wasmcache/flight.v1.wasm",
							},
							HistoryCapSize: 3,
							Template: apiextv1.CustomResourceDefinitionSpec{
								Group: "examples.com",
								Names: apiextv1.CustomResourceDefinitionNames{
									Plural:     "backends",
									Singular:   "backend",
									ShortNames: []string{"be"},
									Kind:       "Backend",
								},
								Scope: apiextv1.NamespaceScoped,
								Versions: []apiextv1.CustomResourceDefinitionVersion{
									{
										Name:    "v1",
										Served:  true,
										Storage: true,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
										},
									},
								},
							},
						},
					}),
				},
				Wait: 30 * time.Second,
				Poll: time.Second,
			})
		},
		time.Second,
		10*time.Second,
		"failed to update airway",
	)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		backend, err = backendIntf.Get(ctx, "test", metav1.GetOptions{})
		if err != nil {
			return err
		}
		unstructured.SetNestedMap(backend.Object, map[string]any{"test": "test"}, "spec", "labels")
		backend, err = backendIntf.Update(ctx, backend, metav1.UpdateOptions{})
		return err
	})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err := client.Clientset.AppsV1().Deployments("default").Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get deployment: %w", err)
			}
			if len(deployment.Labels) == 0 {
				return fmt.Errorf("deployment does not have labels")
			}
			if label := deployment.Labels["test"]; label != "test" {
				return fmt.Errorf("did not see updated label got %q but expected %q", label, "test")
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected to see updated label",
	)

	release, err = client.GetRelease(ctx, atc.ReleaseName(backend), "default")
	require.NoError(t, err)
	require.Len(t, release.History, 3)
}

func TestFixDriftInterval(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "test-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/flight.v1.wasm",
					},
					FixDriftInterval: metav1.Duration{Duration: time.Second / 2},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "backends",
							Singular: "backend",
							Kind:     "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "test-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := airwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "test",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: backendv1.BackendSpec{
					Image:    "yokecd/c4ts:test",
					Replicas: 2,
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "test"}))
	}()

	deploymentIntf := client.Clientset.AppsV1().Deployments("default")

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err := deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if replicas := *deployment.Spec.Replicas; replicas != 2 {
				return fmt.Errorf("expected initial replicas to be 2 but got %d", replicas)
			}

			deployment.Spec.Replicas = ptr.To[int32](1)

			if _, err = deploymentIntf.Update(ctx, deployment, metav1.UpdateOptions{FieldManager: "yoke"}); err != nil {
				return fmt.Errorf("failed to update replicas count to 1: %w", err)
			}

			return nil
		},
		time.Second,
		30*time.Second,
		"failed to drift deployment",
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			dep, err := deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if *dep.Spec.Replicas != 2 {
				return fmt.Errorf("expected replicas to be 2 but got %d", *dep.Spec.Replicas)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected drift to be fixed but was not",
	)
}

func TestStatusReadiness(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "longrunning-airway",
				Flight: yoke.FlightParams{Input: internal.JSONReader(v1alpha1.Airway{
					ObjectMeta: metav1.ObjectMeta{
						Name: "tests.examples.com",
					},
					Spec: v1alpha1.AirwaySpec{
						WasmURLs: v1alpha1.WasmURLs{
							Flight: "http://wasmcache/longrunning.wasm",
						},
						Template: apiextv1.CustomResourceDefinitionSpec{
							Group: "examples.com",
							Names: apiextv1.CustomResourceDefinitionNames{
								Plural:   "tests",
								Singular: "test",
								Kind:     "Test",
							},
							Scope: apiextv1.NamespaceScoped,
							Versions: []apiextv1.CustomResourceDefinitionVersion{
								{
									Name:    "v1",
									Served:  true,
									Storage: true,
									Schema: &apiextv1.CustomResourceValidation{
										OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
									},
								},
							},
						},
					},
				})},
				Wait: 15 * time.Second,
				Poll: time.Second,
			}); err != nil && !internal.IsWarning(err) {
				return err
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to create airway",
	)

	testIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "tests",
		}).
		Namespace("default")

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "longrunning-airway"}))
	}()

	emptyTest := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "examples.com/v1",
			"kind":       "Test",
			"metadata": map[string]any{
				"name": "test",
			},
		},
	}

	_, err = testIntf.Create(ctx, emptyTest, metav1.CreateOptions{})
	require.NoError(t, err)

	var statuses []string
	testutils.EventuallyNoErrorf(
		t,
		func() error {
			resource, err := testIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get test resource: %v", err)
			}

			condition := internal.GetFlightReadyCondition(resource)
			if condition == nil {
				return fmt.Errorf("ready condition not found")
			}

			reason := condition.Reason

			if reason != "" && !slices.Contains(statuses, reason) {
				statuses = append(statuses, reason)
			}
			if reason == "Ready" {
				return nil
			}

			return fmt.Errorf("not ready: %s", reason)
		},
		time.Second,
		15*time.Second,
		"test resource failed to become ready",
	)

	require.EqualValues(t, []string{"InProgress", "Ready"}, statuses)
}

func TestResourceAccessMatchers(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	_, err = client.Clientset.CoreV1().Secrets("default").Create(
		ctx,
		&corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "one",
				Namespace: "default",
			},
			StringData: map[string]string{"key": "secret one"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Secrets("default").Delete(ctx, "one", metav1.DeleteOptions{}))
	}()

	require.NoError(t, client.EnsureNamespace(ctx, "custom"))
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(ctx, "custom", metav1.DeleteOptions{}))
	}()

	_, err = client.Clientset.CoreV1().Secrets("custom").Create(
		ctx,
		&corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "two",
				Namespace: "custom",
			},
			StringData: map[string]string{"key": "secret two"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Secrets("custom").Delete(ctx, "two", metav1.DeleteOptions{}))
	}()

	cases := []struct {
		Name     string
		Matchers []string
		Err      string
	}{
		{
			Name:     "no matchers",
			Matchers: nil,
			Err:      "failed to lookup secret one: forbidden: cannot access resource outside of target release ownership",
		},
		{
			Name:     "matchers that do not match",
			Matchers: []string{"bar/Secret:example"},
			Err:      "failed to lookup secret one: forbidden: cannot access resource outside of target release ownership",
		},
		{
			Name:     "match only first secret",
			Matchers: []string{"default/*"},
			Err:      "failed to lookup secret two: forbidden: cannot access resource outside of target release ownership",
		},
		// {
		// 	Name:     "match both separately",
		// 	Matchers: []string{"default/*", "custom/Secret"},
		// },
		{
			Name:     "match all secrets",
			Matchers: []string{"Secret"},
		},
		{
			Name:     "match all resources",
			Matchers: []string{"*"},
		},
	}

	params := func(matchers []string) yoke.TakeoffParams {
		return yoke.TakeoffParams{
			Release: "resourceaccessmatchers-airway",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(v1alpha1.Airway{
					ObjectMeta: metav1.ObjectMeta{
						Name: "backends.examples.com",
					},
					Spec: v1alpha1.AirwaySpec{
						WasmURLs: v1alpha1.WasmURLs{
							Flight: "http://wasmcache/resourceaccessmatchers.wasm",
						},
						ClusterAccess:          true,
						ResourceAccessMatchers: matchers,
						Template: apiextv1.CustomResourceDefinitionSpec{
							Group: "examples.com",
							Names: apiextv1.CustomResourceDefinitionNames{
								Plural:   "backends",
								Singular: "backend",
								Kind:     "Backend",
							},
							Scope: apiextv1.NamespaceScoped,
							Versions: []apiextv1.CustomResourceDefinitionVersion{
								{
									Name:    "v1",
									Served:  true,
									Storage: true,
									Schema: &apiextv1.CustomResourceValidation{
										OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
									},
								},
							},
						},
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}
	}
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "resourceaccessmatchers-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				list, err := airwayIntf.List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				if count := len(list.Items); count != 0 {
					return fmt.Errorf("expected no error but got %d", count)
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to remove airway",
		)
	}()

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			require.NoError(t, commander.Takeoff(ctx, params(tc.Matchers)))

			backendIntf := client.Dynamic.
				Resource(schema.GroupVersionResource{
					Group:    "examples.com",
					Version:  "v1",
					Resource: "backends",
				}).
				Namespace("default")

			be := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "examples.com/v1",
					"kind":       "Backend",
					"metadata": map[string]any{
						"name":      "test",
						"namespace": "default",
					},
				},
			}

			_, err = backendIntf.Create(ctx, be, metav1.CreateOptions{})
			if tc.Err != "" {
				require.ErrorContains(t, err, tc.Err)
				return
			}
			require.NoError(t, err)

			require.NoError(t, client.WaitForReady(ctx, be, k8s.WaitOptions{Timeout: 30 * time.Second}))

			require.NoError(t, backendIntf.Delete(ctx, be.GetName(), metav1.DeleteOptions{}))

			testutils.EventuallyNoErrorf(
				t,
				func() error {
					list, err := backendIntf.List(ctx, metav1.ListOptions{})
					if err != nil {
						return err
					}
					if count := len(list.Items); count > 0 {
						return fmt.Errorf("listed %d backends instead of none", count)
					}
					return nil
				},
				time.Second,
				30*time.Second,
				"backend count did not go to zero",
			)
		})
	}
}

func TestAirwayModes(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "modes-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/flight.v1.modes.wasm",
					},
					Mode:          v1alpha1.AirwayModeStatic,
					ClusterAccess: true,
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "backends",
							Singular: "backend",
							Kind:     "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "modes-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := airwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "test",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: backendv1.BackendSpec{
					Image:    "yokecd/c4ts:test",
					Replicas: 2,
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "test"}))
	}()

	deploymentIntf := client.Clientset.AppsV1().Deployments("default")

	var deployment *appsv1.Deployment

	testutils.EventuallyNoErrorf(
		t,
		func() (err error) {
			deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			return err
		},
		time.Second,
		30*time.Second,
		"failed to see deployment created",
	)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			deployment.Spec.Replicas = ptr.To[int32](5)
			expectedErr := `admission webhook "resources.yoke.cd" denied the request: cannot modify flight sub-resources`
			deployment, err = deploymentIntf.Update(ctx, deployment, metav1.UpdateOptions{FieldManager: "yoke"})
			if err == nil {
				return fmt.Errorf("expected error but got none")
			}
			if err.Error() != expectedErr {
				return fmt.Errorf("expected error %q but got %v", expectedErr, err)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to have admission webhook deny change",
	)

	require.EqualError(
		t,
		deploymentIntf.Delete(ctx, "test", metav1.DeleteOptions{}),
		`admission webhook "resources.yoke.cd" denied the request: cannot delete resources managed by Air-Traffic-Controller`,
	)

	backendIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "backends",
		}).
		Namespace("default")

	testBE, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
	require.NoError(t, err)

	annotations := testBE.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[flight.AnnotationOverrideMode] = string(v1alpha1.AirwayModeDynamic)

	testBE.SetAnnotations(annotations)

	_, err = backendIntf.Update(ctx, testBE, metav1.UpdateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			be, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}

			condition := internal.GetFlightReadyCondition(be)

			if reason := condition.Reason; reason != "Ready" {
				return fmt.Errorf("expected status to be Ready but got: %s", reason)
			}

			return nil
		},
		time.Second,
		30*time.Second,
		"backend failed to become ready",
	)

	deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
	require.NoError(t, err)

	deployment.Spec.Replicas = ptr.To[int32](8)

	deployment, err = deploymentIntf.Update(ctx, deployment, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 8, *deployment.Spec.Replicas)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if actual := *deployment.Spec.Replicas; actual != 2 {
				return fmt.Errorf("expected replicas to be 2 but got %d", actual)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"deployment failed to self-heal from bad value",
	)

	require.NoError(t, deploymentIntf.Delete(ctx, "test", metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if actual := *deployment.Spec.Replicas; actual != 2 {
				return fmt.Errorf("expected replicas to be 2 but got %d", actual)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"deployment failed to self-heal after deletion",
	)

	configmapIntf := client.Clientset.CoreV1().ConfigMaps("default")

	configmap, err := configmapIntf.Get(ctx, "test", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "", configmap.Data["replicas"])

	// The flight.v1.modes flight lets us set the replicas via the intermediary of our configmap.
	configmap.Data = map[string]string{"replicas": "7"}

	_, err = configmapIntf.Update(ctx, configmap, metav1.UpdateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			deployment, err = deploymentIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if actual := *deployment.Spec.Replicas; actual != 7 {
				return fmt.Errorf("expected replicas to be 7 but got %d", actual)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"deployment failed to reach state as defined by configmap",
	)
}

func TestDynamicWithExternalResource(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	secret, err := client.Clientset.CoreV1().Secrets("default").Create(
		ctx,
		&corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "one",
				Namespace: "default",
			},
			StringData: map[string]string{"key": "secret one"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Secrets("default").Delete(ctx, "one", metav1.DeleteOptions{}))
	}()

	require.NoError(t, client.EnsureNamespace(ctx, "custom"))
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(ctx, "custom", metav1.DeleteOptions{}))
	}()

	_, err = client.Clientset.CoreV1().Secrets("custom").Create(
		ctx,
		&corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "two",
				Namespace: "custom",
			},
			StringData: map[string]string{"key": "secret two"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Clientset.CoreV1().Secrets("custom").Delete(ctx, "two", metav1.DeleteOptions{}))
	}()

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "dynamic-external-resource-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/resourceaccessmatchers.wasm",
					},
					Mode:                   v1alpha1.AirwayModeDynamic,
					ClusterAccess:          true,
					ResourceAccessMatchers: []string{"Secret"},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "backends",
							Singular: "backend",
							Kind:     "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "dynamic-external-resource-airway"}))

		airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
			Group:    "yoke.cd",
			Version:  "v1alpha1",
			Resource: "airways",
		})

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := airwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	configMapIntf := client.Clientset.CoreV1().ConfigMaps("default")

	backendIntf := k8s.
		TypedInterface[EmptyCRD](client.Dynamic, schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "backends",
	}).
		Namespace("default")

	be := &EmptyCRD{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Backend",
			APIVersion: "examples.com/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	_, err = backendIntf.Create(ctx, be, metav1.CreateOptions{})
	require.NoError(t, err)

	readSecretKey := func(value string) string {
		var data struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal([]byte(value), &data)
		result, _ := base64.StdEncoding.DecodeString(data.Key)
		return string(result)
	}

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			cm, err := configMapIntf.Get(ctx, "cm", metav1.GetOptions{})
			if err != nil {
				return err
			}

			expected := "secret one"
			if got := readSecretKey(cm.Data["one"]); got != expected {
				return testutils.Fatal(fmt.Errorf("expected configmap.data[one] to equal %q but got %q", expected, got))
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"error asserting configmap state",
	)

	secret.Data["key"] = []byte("updated")

	_, err = client.Clientset.CoreV1().Secrets("default").Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			cm, err := configMapIntf.Get(ctx, "cm", metav1.GetOptions{})
			if err != nil {
				return err
			}

			expected := "updated"
			if got := readSecretKey(cm.Data["one"]); got != expected {
				return fmt.Errorf("expected configmap.data[one] to equal %q but got %q", expected, got)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"error asserting configmap state",
	)
}

func TestExternalDynamicCreateEvent(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	type Spec struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}

	type Status struct {
		Msg string `json:"msg"`
	}

	type CopyJob struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
		Spec              Spec   `json:"spec"`
		Status            Status `json:"status"`
	}
	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "dynamic-external-creation-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "clones.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/externalcreation.wasm",
					},
					Mode:                   v1alpha1.AirwayModeDynamic,
					ClusterAccess:          true,
					ResourceAccessMatchers: []string{"default/ConfigMap"},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "clones",
							Singular: "clone",
							Kind:     "Clone",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[CopyJob](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "dynamic-external-creation-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.AirwayIntf.Get(ctx, "clones.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("clones.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	configmapIntf := client.Clientset.CoreV1().ConfigMaps("default")

	cloneIntf := k8s.
		TypedInterface[CopyJob](client.Dynamic, schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "clones",
	}).
		Namespace("default")

	be := &CopyJob{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Clone",
			APIVersion: "examples.com/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: Spec{
			Source: "source",
			Target: "target",
		},
	}

	_, err = configmapIntf.Get(ctx, "target", metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))

	_, err = cloneIntf.Create(ctx, be, metav1.CreateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			cm, err := cloneIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			expected := "source does not exist: waiting for it to be created."
			if actual := cm.Status.Msg; actual != expected {
				return fmt.Errorf("expected status.msg to be %q but got %q", expected, actual)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"error asserting Clone state",
	)

	_, err = configmapIntf.Create(
		ctx,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "source"},
			Data:       map[string]string{"test": "data"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			target, err := configmapIntf.Get(ctx, "target", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if target.Data["test"] != "data" {
				return fmt.Errorf("expected target.data.test to be data but got: %v", target.Data["test"])
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"error asserting target configmap state",
	)
}

func TestStatusUpdates(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	type CRStatus struct {
		Potato     string            `json:"potato,omitempty"`
		Conditions flight.Conditions `json:"conditions,omitempty"`
	}

	type CR struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
		Spec              CRStatus `json:"spec"`
		Status            CRStatus `json:"status,omitzero"`
	}

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "status-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/status.wasm",
					},
					Mode:          v1alpha1.AirwayModeStatic,
					ClusterAccess: true,
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "backends",
							Singular: "backend",
							Kind:     "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[CR](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "status-airway"}))
		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.AirwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	backendGVR := schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "backends",
	}

	backendIntf := k8s.TypedInterface[CR](client.Dynamic, backendGVR).Namespace("default")

	cases := []struct {
		Name        string
		Spec        CRStatus
		Expectation func(t *testing.T)
	}{
		{
			Name: "top level property",
			Spec: CRStatus{Potato: "peels"},
			Expectation: func(t *testing.T) {
				testutils.EventuallyNoErrorf(
					t,
					func() error {
						be, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
						if err != nil {
							return err
						}
						ready := meta.FindStatusCondition(be.Status.Conditions, "Ready")
						if ready == nil {
							return fmt.Errorf("ready condition not found")
						}
						if ready.Status != metav1.ConditionTrue {
							return fmt.Errorf("ready condition should be true but got false")
						}
						if value := be.Status.Potato; value != "peels" {
							return fmt.Errorf("expected potato to have peels: but got: %q", value)
						}
						return nil
					},
					time.Second,
					30*time.Second,
					"did not get expected status",
				)
			},
		},
		{
			Name: "ready set to false",
			Spec: CRStatus{
				Conditions: flight.Conditions{
					{
						Type:               "Ready",
						Status:             metav1.ConditionFalse,
						Reason:             "Custom",
						LastTransitionTime: metav1.Now(),
						Message:            "not feeling it.",
					},
				},
			},
			Expectation: func(t *testing.T) {
				testutils.EventuallyNoErrorf(
					t,
					func() error {
						be, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
						if err != nil {
							return err
						}
						readyCondition := meta.FindStatusCondition(be.Spec.Conditions, "Ready")
						if readyCondition == nil {
							return fmt.Errorf("no ready condition set")
						}
						if msg := readyCondition.Message; msg != "not feeling it." {
							return fmt.Errorf("expected ready condition message to be %q but got %q", "not feeling it.", msg)
						}
						return nil
					},
					time.Second,
					30*time.Second,
					"did not get expected status",
				)
			},
		},
		{
			Name: "with custom condition",
			Spec: CRStatus{
				Conditions: flight.Conditions{
					{
						Type:               "Custom",
						Status:             metav1.StatusSuccess,
						Reason:             "Test",
						Message:            "ok",
						LastTransitionTime: metav1.Now(),
					},
				},
			},
			Expectation: func(t *testing.T) {
				testutils.EventuallyNoErrorf(
					t,
					func() error {
						be, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
						if err != nil {
							return err
						}
						if count := len(be.Status.Conditions); count != 2 {
							return fmt.Errorf("expected two conditions but %d", count)
						}
						for _, status := range []string{"Custom", "Ready"} {
							if meta.FindStatusCondition(be.Status.Conditions, status) == nil {
								return fmt.Errorf("no %q condition found", status)
							}
						}
						return nil
					},
					time.Second,
					30*time.Second,
					"did not get expected status",
				)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			backendIntf.Delete(ctx, "test", metav1.DeleteOptions{})

			testutils.EventuallyNoErrorf(
				t,
				func() error {
					list, err := backendIntf.List(ctx, metav1.ListOptions{})
					if err != nil {
						return err
					}
					if len(list) != 0 {
						return fmt.Errorf("does not have 0 existing test resources: state unclean")
					}
					return nil
				},
				time.Second,
				10*time.Second,
				"previous test still exists",
			)

			be := CR{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "examples.com/v1",
					Kind:       "Backend",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tc.Spec,
			}

			_, err = backendIntf.Create(ctx, &be, metav1.CreateOptions{})
			require.NoError(t, err)

			tc.Expectation(t)
		})
	}
}

func TestDeploymentStatus(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	type CRStatus struct {
		Conditions    flight.Conditions `json:"conditions,omitempty"`
		AvailablePods string            `json:"availablePods,omitempty"`
	}

	type CR struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
		Image             string   `json:"image"`
		Status            CRStatus `json:"status,omitzero"`
	}

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "deploymentstatus-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/deploymentstatus.wasm",
					},
					Mode:          v1alpha1.AirwayModeDynamic,
					ClusterAccess: true,
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "backends",
							Singular: "backend",
							Kind:     "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[CR](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "deploymentstatus-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.AirwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	backendGVR := schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "backends",
	}

	backendIntf := k8s.TypedInterface[CR](client.Dynamic, backendGVR).Namespace("default")

	be := &CR{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "examples.com/v1",
			Kind:       "Backend",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Image: "yokecd/c4ts:test",
	}

	be, err = backendIntf.Create(ctx, be, metav1.CreateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			be, err := backendIntf.Get(ctx, be.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if ap := be.Status.AvailablePods; ap != "1" {
				return fmt.Errorf("expected one available pod but got: %q", ap)
			}
			ready := meta.FindStatusCondition(be.Status.Conditions, "Ready")
			if ready == nil {
				return fmt.Errorf("expected ready condition to be presented but was not")
			}
			if ready.Status != metav1.ConditionTrue {
				return fmt.Errorf("expected ready condition to be true but got false with message: %v", ready.Message)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"failed to get available pods",
	)
}

func TestPruning(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	airwayParams := func(prune v1alpha1.PruneOptions) yoke.TakeoffParams {
		return yoke.TakeoffParams{
			Release: "prune-airway",
			Flight: yoke.FlightParams{
				Input: internal.JSONReader(v1alpha1.Airway{
					ObjectMeta: metav1.ObjectMeta{
						Name: "tests.examples.com",
					},
					Spec: v1alpha1.AirwaySpec{
						WasmURLs: v1alpha1.WasmURLs{
							Flight: "http://wasmcache/prune.wasm",
						},
						Mode:          v1alpha1.AirwayModeDynamic,
						Prune:         prune,
						ClusterAccess: true,
						Template: apiextv1.CustomResourceDefinitionSpec{
							Group: "examples.com",
							Names: apiextv1.CustomResourceDefinitionNames{
								Plural:   "tests",
								Singular: "test",
								Kind:     "Test",
							},
							Scope: apiextv1.ClusterScoped,
							Versions: []apiextv1.CustomResourceDefinitionVersion{
								{
									Name:    "v1",
									Served:  true,
									Storage: true,
									Schema: &apiextv1.CustomResourceValidation{
										OpenAPIV3Schema: openapi.SchemaFor[struct{}](),
									},
								},
							},
						},
					},
				}),
			},
			Wait: 30 * time.Second,
			Poll: time.Second,
		}
	}

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "prune-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
					Group:    "yoke.cd",
					Version:  "v1alpha1",
					Resource: "airways",
				})
				_, err := airwayIntf.Get(ctx, "tests.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("tests.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"clear all prunes!",
		)
	}()

	require.NoError(t, commander.Takeoff(ctx, airwayParams(v1alpha1.PruneOptions{})))

	testIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "tests",
	})

	nsIntf := client.Clientset.CoreV1().Namespaces()

	crdIntf := client.Dynamic.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	})

	resource, err := testIntf.Create(
		ctx,
		&unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "examples.com/v1",
				"kind":       "Test",
				"metadata": map[string]any{
					"name": "test",
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	expectedOwner := "default/examples.com.Test.test"

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			ns, err := nsIntf.Get(ctx, "prune", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected namespace: %w", err)
			}
			nsUnstructured, _ := internal.ToUnstructured(ns)
			if owner := internal.GetOwner(nsUnstructured); owner != expectedOwner {
				return fmt.Errorf("expected owner to be %q but got %q", expectedOwner, owner)
			}
			crd, err := crdIntf.Get(ctx, "prunes.test.com", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected crd: %w", err)
			}
			if owner := internal.GetOwner(crd); owner != expectedOwner {
				return fmt.Errorf("expected owner to be %q but got %q", expectedOwner, owner)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"package state not as expected",
	)

	require.NoError(t, testIntf.Delete(ctx, resource.GetName(), metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			ns, err := nsIntf.Get(ctx, "prune", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected namespace: %w", err)
			}
			nsUnstructured, _ := internal.ToUnstructured(ns)
			if owner := internal.GetOwner(nsUnstructured); owner != "" {
				return fmt.Errorf("expected no owner but got: %q", owner)
			}
			crd, err := crdIntf.Get(ctx, "prunes.test.com", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected crd: %w", err)
			}
			if owner := internal.GetOwner(crd); owner != "" {
				return fmt.Errorf("expected no owner but got: %q", owner)
			}
			return nil
		},
		time.Second/2,
		30*time.Second,
		"expected resources without owners",
	)

	require.NoError(t, commander.Takeoff(ctx, airwayParams(v1alpha1.PruneOptions{CRDs: true, Namespaces: true})))

	resource, err = testIntf.Create(
		ctx,
		&unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "examples.com/v1",
				"kind":       "Test",
				"metadata": map[string]any{
					"name": "test",
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			ns, err := nsIntf.Get(ctx, "prune", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected namespace: %w", err)
			}
			nsUnstructured, _ := internal.ToUnstructured(ns)
			if owner := internal.GetOwner(nsUnstructured); owner != expectedOwner {
				return fmt.Errorf("expected owner to be %q but got %q", expectedOwner, owner)
			}
			crd, err := crdIntf.Get(ctx, "prunes.test.com", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected crd: %w", err)
			}
			if owner := internal.GetOwner(crd); owner != expectedOwner {
				return fmt.Errorf("expected owner to be %q but got %q", expectedOwner, owner)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"package state not as expected",
	)

	require.NoError(t, testIntf.Delete(ctx, resource.GetName(), metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if _, err := nsIntf.Get(ctx, "prune", metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected error not found but got: %v", err)
			}
			if _, err := crdIntf.Get(ctx, "prunes.test.com", metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected error not found but got: %v", err)
			}
			return nil
		},
		time.Second,
		time.Minute,
		"expected resources without owners",
	)
}

func TestOverridePermissions(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	sa, err := client.Clientset.CoreV1().ServiceAccounts("default").Create(
		context.Background(),
		&corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ServiceAccount",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	role, err := client.Clientset.RbacV1().Roles("default").Create(
		context.Background(),
		&rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.Identifier(),
				Kind:       "Role",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "backender",
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"*"},
					APIGroups: []string{"examples.com"},
					Resources: []string{"backends"},
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	t.Log(role.TypeMeta)

	_, err = client.Clientset.RbacV1().RoleBindings("default").Create(
		context.Background(),
		&rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RoleBinding",
				APIVersion: rbacv1.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-backender",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.Name,
					Namespace: sa.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: role.GroupVersionKind().Group,
				Kind:     "Role",
				Name:     role.Name,
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	restCfg, err := clientcmd.BuildConfigFromFlags("", home.Kubeconfig)
	require.NoError(t, err)

	restCfg.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:default:" + sa.Name,
	}

	saClient, err := k8s.NewClient(restCfg, "")
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), func(value bool) *bool { return &value }(true))

	commander := yoke.FromK8Client(client)

	airwayTakeoffParams := yoke.TakeoffParams{
		Release: "backend-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/flight.v1.wasm",
					},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:     "backends",
							Singular:   "backend",
							ShortNames: []string{"be"},
							Kind:       "Backend",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}

	require.NoError(t, commander.Takeoff(ctx, airwayTakeoffParams))

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "backend-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.AirwayIntf.Get(ctx, "backends.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("backends.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"cleanup not successful",
		)
	}()

	be, err := internal.ToUnstructured(&backendv1.Backend{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backend",
			Annotations: map[string]string{
				flight.AnnotationOverrideFlight: "https://evil.attack",
			},
		},
		Spec: backendv1.BackendSpec{
			Image:    "yokecd/c4ts:test",
			Replicas: 2,
		},
	})
	require.NoError(t, err)

	beIntf := saClient.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "backends",
		}).
		Namespace("default")

	_, err = beIntf.Create(ctx, be, metav1.CreateOptions{})
	require.ErrorContains(t, err, `admission webhook "backends.examples.com" denied the request: user does not have permissions to create or update override annotations`)

	be.SetAnnotations(map[string]string{})

	be, err = beIntf.Create(ctx, be, metav1.CreateOptions{})
	require.NoError(t, err)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		resource, err := beIntf.Get(context.Background(), be.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		resource.SetAnnotations(map[string]string{flight.AnnotationOverrideMode: string(v1alpha1.AirwayModeDynamic)})
		_, err = beIntf.Update(context.Background(), resource, metav1.UpdateOptions{})
		return err
	})
	require.ErrorContains(t, err, `admission webhook "backends.examples.com" denied the request: user does not have permissions to create or update override annotations`)
}

func TestTimeout(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "timeout-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "timeouts.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/timeout.wasm",
					},
					Timeout: metav1.Duration{Duration: 30 * time.Millisecond},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "examples.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Plural:   "timeouts",
							Singular: "timeout",
							Kind:     "Timeout",
						},
						Scope: apiextv1.NamespaceScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
								},
							},
						},
					},
				},
			}),
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}))

	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "timeout-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				_, err := client.AirwayIntf.Get(ctx, "timeouts.examples.com", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("timeouts.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to test resources",
		)
	}()

	timeoutIntf := k8s.
		TypedInterface[EmptyCRD](client.Dynamic, schema.GroupVersionResource{
		Group:    "examples.com",
		Version:  "v1",
		Resource: "timeouts",
	}).
		Namespace("default")

	instance := EmptyCRD{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Timeout",
			APIVersion: "examples.com/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	_, err = timeoutIntf.Create(ctx, &instance, metav1.CreateOptions{})
	require.ErrorContains(t, err, "module closed with context deadline exceeded: execution timeout (30ms) exceeded")
}

func TestSubscriptionMode(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "subscription-airway",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(v1alpha1.Airway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "subscriptions.example.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURLs: v1alpha1.WasmURLs{
						Flight: "http://wasmcache/subscriptions.wasm",
					},
					Mode:                   v1alpha1.AirwayModeSubscription,
					ClusterAccess:          true,
					ResourceAccessMatchers: []string{"ConfigMap:external"},
					Template: apiextv1.CustomResourceDefinitionSpec{
						Group: "example.com",
						Names: apiextv1.CustomResourceDefinitionNames{
							Kind:     "Subscription",
							Plural:   "subscriptions",
							Singular: "subscription",
						},
						Scope: apiextv1.ClusterScoped,
						Versions: []apiextv1.CustomResourceDefinitionVersion{
							{
								Name:    "v1",
								Served:  true,
								Storage: true,
								Schema: &apiextv1.CustomResourceValidation{
									OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
								},
							},
						},
					},
				},
			}),
		},
		Poll: time.Second,
		Wait: 30 * time.Second,
	}))
	defer func() {
		require.NoError(t, commander.Mayday(ctx, yoke.MaydayParams{Release: "subscription-airway"}))

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if _, err := client.AirwayIntf.Get(ctx, "subscriptions.examples.com", metav1.GetOptions{}); err == nil {
					return fmt.Errorf("subscriptions.examples.com has not been removed")
				}
				if !kerrors.IsNotFound(err) {
					return err
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"failed to remove subscription-airway",
		)
	}()

	subIntf := k8s.TypedInterface[EmptyCRD](client.Dynamic, schema.GroupVersionResource{
		Group:    "example.com",
		Version:  "v1",
		Resource: "subscriptions",
	})

	cmIntf := client.Clientset.CoreV1().ConfigMaps("default")

	external, err := cmIntf.Create(
		ctx,
		&corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "external"},
			Data:       map[string]string{"key": "value"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	sub := &EmptyCRD{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "example.com/v1",
			Kind:       "Subscription",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}

	_, err = subIntf.Create(ctx, sub, metav1.CreateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			for _, name := range []string{"subscribed", "standard"} {
				if _, err := cmIntf.Get(ctx, name, metav1.GetOptions{}); err != nil {
					return fmt.Errorf("failed to get configmap %s: %w", name, err)
				}
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected configmaps to be created",
	)

	require.NoError(t, cmIntf.Delete(ctx, "standard", metav1.DeleteOptions{}))

	var seen int
	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if _, err := cmIntf.Get(ctx, "standard", metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected standard to be missing but got: %w", err)
			}
			seen++
			if seen < 5 {
				return fmt.Errorf("expected standard to be missing for at least 5 seconds")
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected standard to be deleted.",
	)

	require.NoError(t, cmIntf.Delete(ctx, "subscribed", metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			for _, name := range []string{"subscribed", "standard"} {
				if _, err := cmIntf.Get(ctx, name, metav1.GetOptions{}); err != nil {
					return fmt.Errorf("failed to get configmap %s: %w", name, err)
				}
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected configmaps to be resynced",
	)

	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		external, err = cmIntf.Get(ctx, external.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		external.Data = map[string]string{"key": "updated"}
		_, err = cmIntf.Update(ctx, external, metav1.UpdateOptions{})
		return err
	}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			standard, err := cmIntf.Get(ctx, "standard", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get configmap %s: %w", "standard", err)
			}
			if standard.Data["key"] != "updated" {
				return fmt.Errorf("standard configmap did not update: %w", err)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected configmaps to be resynced",
	)
}

func TestValidationCycle(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	airway, err := client.AirwayIntf.Create(
		context.Background(),
		&v1alpha1.Airway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backends.examples.com",
			},
			Spec: v1alpha1.AirwaySpec{
				WasmURLs: v1alpha1.WasmURLs{
					Flight: "http://wasmcache/flight.v1.wasm",
				},
				Template: apiextv1.CustomResourceDefinitionSpec{
					Group: "examples.com",
					Names: apiextv1.CustomResourceDefinitionNames{
						Plural:     "backends",
						Singular:   "backend",
						ShortNames: []string{"be"},
						Kind:       "Backend",
					},
					Scope: apiextv1.NamespaceScoped,
					Versions: []apiextv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1",
							Served:  true,
							Storage: true,
							Schema: &apiextv1.CustomResourceValidation{
								OpenAPIV3Schema: openapi.SchemaFor[backendv1.Backend](),
							},
						},
					},
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, client.AirwayIntf.Delete(context.Background(), airway.Name, metav1.DeleteOptions{}))
		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if _, err := client.AirwayIntf.Get(context.Background(), airway.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected error to be not found but got: %w", err)
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"expected airway to be deleted proper",
		)
	}()

	require.NoError(t,
		client.WaitForReady(context.Background(), internal.Must2(internal.ToUnstructured(airway)), k8s.WaitOptions{
			Timeout:  30 * time.Second,
			Interval: time.Second,
		}),
	)

	require.NoError(t, client.EnsureNamespace(context.Background(), "foo"))

	backendIntf := k8s.TypedInterface[backendv1.Backend](client.Dynamic, schema.GroupVersionResource{
		Resource: "backends",
		Group:    "examples.com",
		Version:  "v1",
	}).Namespace("foo")

	be, err := backendIntf.Create(
		context.Background(),
		&backendv1.Backend{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cycle",
			},
			Spec: backendv1.BackendSpec{
				Image:    "yokecd/c4ts:test",
				Replicas: 1,
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			_, err := backendIntf.Get(context.Background(), be.Name, metav1.GetOptions{})
			return err
		},
		time.Second,
		10*time.Second,
		"failed to get backend",
	)

	require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(context.Background(), "foo", metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if _, err := backendIntf.Get(context.Background(), be.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected error not found but got: %w", err)
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected backend to be deleted with namespace",
	)
}

func TestIdentityWithError(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	type CR struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
		Status            struct {
			Message string `json:"message,omitempty"`
		} `json:"status"`
	}

	airway, err := client.AirwayIntf.Create(
		context.Background(),
		&v1alpha1.Airway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tests.examples.com",
			},
			Spec: v1alpha1.AirwaySpec{
				WasmURLs: v1alpha1.WasmURLs{
					Flight: "http://wasmcache/identityerror.wasm",
				},
				Mode:          v1alpha1.AirwayModeSubscription,
				ClusterAccess: true,
				Template: apiextv1.CustomResourceDefinitionSpec{
					Group: "examples.com",
					Names: apiextv1.CustomResourceDefinitionNames{
						Plural:   "tests",
						Singular: "test",
						Kind:     "Test",
					},
					Scope: apiextv1.NamespaceScoped,
					Versions: []apiextv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1",
							Served:  true,
							Storage: true,
							Schema: &apiextv1.CustomResourceValidation{
								OpenAPIV3Schema: openapi.SchemaFor[CR](),
							},
						},
					},
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	require.NoError(t,
		client.WaitForReady(context.Background(), internal.Must2(internal.ToUnstructured(airway)), k8s.WaitOptions{
			Timeout:  30 * time.Second,
			Interval: time.Second,
		}),
	)

	testGVR := schema.GroupVersionResource{
		Resource: "tests",
		Group:    "examples.com",
		Version:  "v1",
	}

	testIntf := k8s.TypedInterface[CR](client.Dynamic, testGVR).Namespace("default")

	test, err := testIntf.Create(
		context.Background(),
		&CR{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Test",
				APIVersion: "examples.com/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "identity",
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	defer func() {
		// Kubernetes garbage collectin is too slow and creating flaky test conditions...
		// So forcefully remove test resource before deleting airway...
		require.NoError(t, testIntf.Delete(context.Background(), test.Name, metav1.DeleteOptions{}))
		require.NoError(t, client.WaitIsRemovedFromCluster(context.Background(), internal.Must2(internal.ToUnstructured(test)), k8s.WaitOptions{}))

		require.NoError(t, client.AirwayIntf.Delete(context.Background(), airway.Name, metav1.DeleteOptions{}))
		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if _, err := client.AirwayIntf.Get(context.Background(), airway.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected error to be not found but got: %v", err)
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"expected airway to be deleted proper",
		)
	}()

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			test, err := testIntf.Get(context.Background(), test.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if msg := test.Status.Message; msg != "artificial test error" {
				return fmt.Errorf("expected the artifical test error but got: %q", msg)
			}
			_, err = client.Clientset.AppsV1().Deployments("default").Get(context.Background(), test.Name, metav1.GetOptions{})
			return err
		},
		time.Second,
		10*time.Second,
		"failed to get test with expected state",
	)
}

func TestInvalidFlightURL(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	_, err = client.AirwayIntf.Create(
		context.Background(),
		&v1alpha1.Airway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tests.examples.com",
			},
			Spec: v1alpha1.AirwaySpec{
				WasmURLs: v1alpha1.WasmURLs{
					Flight: "http://evil/main.wasm",
				},
				Mode:          v1alpha1.AirwayModeSubscription,
				ClusterAccess: true,
				Template: apiextv1.CustomResourceDefinitionSpec{
					Group: "examples.com",
					Names: apiextv1.CustomResourceDefinitionNames{
						Plural:   "tests",
						Singular: "test",
						Kind:     "Test",
					},
					Scope: apiextv1.NamespaceScoped,
					Versions: []apiextv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1",
							Served:  true,
							Storage: true,
							Schema: &apiextv1.CustomResourceValidation{
								OpenAPIV3Schema: openapi.SchemaFor[EmptyCRD](),
							},
						},
					},
				},
			},
		},
		metav1.CreateOptions{},
	)
	require.EqualError(t, err, `admission webhook "airways.yoke.cd" denied the request: module "http://evil/main.wasm" not allowed`)
}
