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
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	backendv1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	backendv2 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v2"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

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
			Namespace: "atc",
		},
		CreateNamespace: true,
		Wait:            30 * time.Second,
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
			if status, _, _ := unstructured.NestedString(resource.Object, "status", "status"); status != "Ready" {
				return fmt.Errorf("expected airway to be Ready but got: %s", status)
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

	require.NoError(t, commander.Mayday(ctx, "c4ts", "default"))

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
		require.NoError(t, commander.Mayday(ctx, "backend-airway", ""))

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

	type EmptyCRD struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
	}

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
		require.NoError(t, commander.Mayday(ctx, "crossnamespace-airway", ""))

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
					FixDriftInterval: openapi.Duration(time.Second / 2),
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
		require.NoError(t, commander.Mayday(ctx, "test-airway", ""))

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
		require.NoError(t, commander.Mayday(ctx, "test", "default"))
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

	type EmptyCRD struct {
		metav1.TypeMeta
		metav1.ObjectMeta `json:"metadata"`
	}

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
		require.NoError(t, commander.Mayday(ctx, "longrunning-airway", ""))
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

			status, _, _ := unstructured.NestedString(resource.Object, "status", "status")
			if status != "" && !slices.Contains(statuses, status) {
				statuses = append(statuses, status)
			}
			if status == "Ready" {
				return nil
			}

			return fmt.Errorf("not ready: %s", status)
		},
		time.Second,
		15*time.Second,
		"test resource failed to become ready",
	)

	require.EqualValues(t, []string{"InProgress", "Ready"}, statuses)
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
		require.NoError(t, commander.Mayday(ctx, "modes-airway", ""))

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
		require.NoError(t, commander.Mayday(ctx, "test", "default"))
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
			deployment, err = deploymentIntf.Update(ctx, deployment, metav1.UpdateOptions{FieldManager: "test"})
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

	_, err = backendIntf.Update(ctx, testBE, metav1.UpdateOptions{FieldManager: "test"})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			be, err := backendIntf.Get(ctx, "test", metav1.GetOptions{})
			if err != nil {
				return err
			}
			if status, _, _ := unstructured.NestedString(be.Object, "status", "status"); status != "Ready" {
				return fmt.Errorf("expected status to be Ready but got: %s", status)
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

	deployment, err = deploymentIntf.Update(ctx, deployment, metav1.UpdateOptions{FieldManager: "test"})
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

	_, err = configmapIntf.Update(ctx, configmap, metav1.UpdateOptions{FieldManager: "test"})
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
