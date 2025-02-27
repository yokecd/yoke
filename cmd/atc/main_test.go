package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

func TestAirTrafficController(t *testing.T) {
	require.NoError(t, os.RemoveAll("./test_output"))
	require.NoError(t, os.MkdirAll("./test_output", 0o755))

	require.NoError(t, x.X("kind delete clusters --all"))

	require.NoError(t, x.X("kind create cluster --name=atc-test --config -", x.Input(strings.NewReader(`
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
		require.NoError(t, x.X("kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml"))
		require.NoError(t, x.X(`kubectl patch -n kube-system deployment metrics-server --type=json -p [{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]`))
	}

	require.NoError(t, x.X(
		"go build -o ./test_output/atc-installer.wasm ../atc-installer",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))
	require.NoError(t, x.X(
		"go build -o ./test_output/backend.v1.wasm ./internal/testing/apis/backend/v1/flight",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))

	require.NoError(t, x.X(
		"docker build -t yokecd/atc:test -f Dockerfile.atc .",
		x.Dir("../.."),
	))
	require.NoError(t, x.X("kind load --name=atc-test docker-image yokecd/atc:test"))

	require.NoError(t, x.X("docker build -t yokecd/wasmcache:test -f ./internal/testing/Dockerfile.wasmcache ../.."))
	require.NoError(t, x.X("kind load --name=atc-test docker-image yokecd/wasmcache:test"))

	require.NoError(t, x.X("docker build -t yokecd/c4ts:test -f ./internal/testing/Dockerfile.c4ts ./internal/testing"))
	require.NoError(t, x.X("kind load --name=atc-test docker-image yokecd/c4ts:test"))

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	ctx := internal.WithDebugFlag(context.Background(), func(value bool) *bool { return &value }(true))

	commander := yoke.FromK8Client(client)

	atcTakeoffParams := yoke.TakeoffParams{
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
	}

	require.NoError(t, commander.Takeoff(ctx, atcTakeoffParams))

	wasmcacheTakeoffParams := yoke.TakeoffParams{
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
		Wait: 30 * time.Second,
		Poll: time.Second,
	}

	require.NoError(t, commander.Takeoff(ctx, wasmcacheTakeoffParams))

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

	airwayTakeoffParams = yoke.TakeoffParams{
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
	}

	require.NoError(t, commander.Takeoff(ctx, airwayTakeoffParams))

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
}
