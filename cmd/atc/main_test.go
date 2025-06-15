package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	backendv1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	backendv2 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v2"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
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

	commander, err := yoke.FromKubeConfig(home.Kubeconfig)
	if err != nil {
		panic(err)
	}

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	must(commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "atc",
		Flight: yoke.FlightParams{
			Path: "./test_output/atc-installer.wasm",
			Input: strings.NewReader(`{
        "image": "yokecd/atc",
        "version": "test"
      }`),
			Args:      []string{"--skip-version-check"},
			Namespace: "atc",
		},
		CreateNamespace: true,
		Wait:            120 * time.Second,
		Poll:            time.Second,
	}))

	must(commander.Takeoff(ctx, yoke.TakeoffParams{
		Release: "wasmcache",
		Flight: yoke.FlightParams{
			Path: "./test_output/backend.v1.wasm",
			Input: testutils.JsonReader(backendv1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name: "wasmcache",
				},
				Spec: backendv1.BackendSpec{
					Image:    "yokecd/wasmcache:test",
					Replicas: 1,
				},
			}),
			Namespace: "atc",
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
				Input: testutils.JsonReader(v1alpha1.Airway{
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
										OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
			Input: testutils.JsonReader(backendv1.Backend{
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
			Input: testutils.JsonReader(backendv1.Backend{
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
					Input: testutils.JsonReader(v1alpha1.Airway{
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
											OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
										},
									},
									{
										Name:    "v2",
										Served:  true,
										Storage: true,
										Schema: &apiextv1.CustomResourceValidation{
											OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv2.Backend]()),
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
				Input: testutils.JsonReader(backendv1.Backend{
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
				Input: testutils.JsonReader(backendv2.Backend{
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
				Input: testutils.JsonReader(backendv2.Backend{
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
					Input: testutils.JsonReader(v1alpha1.Airway{
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
											OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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

	for _, scale := range []int32{0, 1} {
		atc, err := client.Clientset.AppsV1().Deployments("atc").Get(ctx, "atc-atc", metav1.GetOptions{})
		require.NoError(t, err)

		atc.Spec.Replicas = ptr.To(scale)

		_, err = client.Clientset.AppsV1().Deployments("atc").Update(ctx, atc, metav1.UpdateOptions{FieldManager: "yoke"})
		require.NoError(t, err)

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				pods, err := client.Clientset.CoreV1().Pods("atc").List(ctx, metav1.ListOptions{
					LabelSelector: "yoke.cd/app=atc",
				})
				if err != nil {
					return err
				}
				if count := len(pods.Items); count != int(scale) {
					return fmt.Errorf("expected %d pods but got %d", scale, count)
				}
				return nil
			},
			time.Second,
			30*time.Second,
			"pods did not scale to 0",
		)
	}

	// Connection issue when attempting webhook. Therefore wrap in an eventually.
	// TODO: investigate webhook readiness
	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "example",
				Flight: yoke.FlightParams{
					Namespace: "default",
					Input: testutils.JsonReader(backendv1.Backend{
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
				CrossNamespace: crossNamespace,
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
								OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[EmptyCRD]()),
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
				Flight:  yoke.FlightParams{Input: testutils.JsonReader(airwayWithCrossNamespace(false))},
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

	testIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "tests",
		}).
		Namespace("default")

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
	require.ErrorContains(t, err, "Multiple namespaces detected (if desired enable multinamespace releases)")

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if err := commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "crossnamespace-airway",
				Flight:  yoke.FlightParams{Input: testutils.JsonReader(airwayWithCrossNamespace(true))},
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

	_, err = testIntf.Create(ctx, emptyTest, metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(
		t,
		client.Dynamic.
			Resource(schema.GroupVersionResource{
				Group:    "yoke.cd",
				Version:  "v1alpha1",
				Resource: "airways",
			}).
			Delete(context.Background(), "tests.examples.com", metav1.DeleteOptions{}),
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
					Input: testutils.JsonReader(v1alpha1.Airway{
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
											OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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

	backend, err := internal.ToUnstructured(backendv1.Backend{
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
					Input: testutils.JsonReader(v1alpha1.Airway{
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
											OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
			Input: testutils.JsonReader(backendv1.Backend{
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
				Flight: yoke.FlightParams{Input: testutils.JsonReader(v1alpha1.Airway{
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
										OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[EmptyCRD]()),
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
				Input: testutils.JsonReader(v1alpha1.Airway{
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
										OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[EmptyCRD]()),
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
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
			Input: testutils.JsonReader(backendv1.Backend{
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
		3*time.Second,
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

func TestStatusUpdates(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), ptr.To(true))

	commander := yoke.FromK8Client(client)

	type CRStatus struct {
		Potato     string            `json:"potato"`
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
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[CR]()),
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

	backendIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "backends",
		}).
		Namespace("default")

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
						readyCondition := internal.GetFlightReadyCondition(be)
						if readyCondition == nil || readyCondition.Status != metav1.ConditionTrue {
							return fmt.Errorf("ready condition not met")
						}
						if value, _, _ := unstructured.NestedString(be.Object, "status", "potato"); value != "peels" {
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
						readyCondition := internal.GetFlightReadyCondition(be)
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

						conditions := internal.GetFlightConditions(be)
						if count := len(conditions); count != 2 {
							return fmt.Errorf("expected two conditions but %d", count)
						}
						if !slices.ContainsFunc(conditions, func(cond metav1.Condition) bool { return cond.Type == "Ready" }) {
							return fmt.Errorf("no Ready condition found")
						}
						if !slices.ContainsFunc(conditions, func(cond metav1.Condition) bool { return cond.Type == "Custom" }) {
							return fmt.Errorf("no Custom condition found")
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
					if len(list.Items) != 0 {
						return fmt.Errorf("does not have 0 existing test resources: state unclean")
					}
					return nil
				},
				time.Second,
				10*time.Second,
				"previous test still exists",
			)

			be, err := internal.ToUnstructured(CR{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "examples.com/v1",
					Kind:       "Backend",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: tc.Spec,
			})
			require.NoError(t, err)

			_, err = backendIntf.Create(ctx, be, metav1.CreateOptions{})
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
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[CR]()),
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

	backendIntf := client.Dynamic.
		Resource(schema.GroupVersionResource{
			Group:    "examples.com",
			Version:  "v1",
			Resource: "backends",
		}).
		Namespace("default")

	be, err := internal.ToUnstructured(CR{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "examples.com/v1",
			Kind:       "Backend",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Image: "yokecd/c4ts:test",
	})
	require.NoError(t, err)

	_, err = backendIntf.Create(ctx, be, metav1.CreateOptions{})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			be, err := backendIntf.Get(ctx, be.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			ap, _, _ := unstructured.NestedString(be.Object, "status", "availablePods")
			if ap != "1" {
				return fmt.Errorf("expected one available pod but got: %q", ap)
			}
			if ready := internal.GetFlightReadyCondition(be); ready == nil || ready.Status != metav1.ConditionTrue {
				return fmt.Errorf("expected ready condition to be true")
			}
			return nil
		},
		time.Second,
		10*time.Second,
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
				Input: testutils.JsonReader(v1alpha1.Airway{
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
										OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[struct{}]()),
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

	saClient, err := k8s.NewClient(restCfg)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), func(value bool) *bool { return &value }(true))

	commander := yoke.FromK8Client(client)

	airwayTakeoffParams := yoke.TakeoffParams{
		Release: "backend-airway",
		Flight: yoke.FlightParams{
			Input: testutils.JsonReader(v1alpha1.Airway{
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
									OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[backendv1.Backend]()),
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
				airwayIntf := client.Dynamic.Resource(schema.GroupVersionResource{
					Group:    "yoke.cd",
					Version:  "v1alpha1",
					Resource: "airways",
				})
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
			"cleanup not successful",
		)
	}()

	be, err := internal.ToUnstructured(backendv1.Backend{
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

	be.SetAnnotations(map[string]string{
		flight.AnnotationOverrideMode: string(v1alpha1.AirwayModeDynamic),
	})

	_, err = beIntf.Update(ctx, be, metav1.UpdateOptions{})
	require.ErrorContains(t, err, `admission webhook "backends.examples.com" denied the request: user does not have permissions to create or update override annotations`)
}
