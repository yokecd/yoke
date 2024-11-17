package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/davidmdm/ansi"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	backendv1 "github.com/yokecd/yoke/cmd/atc/internal/testing/apis/backend/v1"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestAirTrafficController(t *testing.T) {
	require.NoError(t, os.RemoveAll("./test_output"))
	require.NoError(t, os.MkdirAll("./test_output", 0o755))

	require.NoError(t, x("kind delete clusters --all"))
	require.NoError(t, x("kind create cluster --name=atc-test"))

	require.NoError(t, x(
		"go build -o ./test_output/atc-installer.wasm ../atc-installer",
		env("GOOS=wasip1", "GOARCH=wasm"),
	))
	require.NoError(t, x(
		"go build -o ./test_output/backend.wasm ./internal/testing/apis/backend/flight",
		env("GOOS=wasip1", "GOARCH=wasm"),
	))

	require.NoError(t, x(
		"docker build -t yokecd/atc:test -f Dockerfile.atc .",
		dir("../.."),
	))
	require.NoError(t, x("kind load --name=atc-test docker-image yokecd/atc:test"))

	require.NoError(t, x("docker build -t yokecd/wasmcache:test -f ./internal/testing/Dockerfile.wasmcache ./internal/testing"))
	require.NoError(t, x("kind load --name=atc-test docker-image yokecd/wasmcache:test"))

	require.NoError(t, x("docker build -t yokecd/c4ts:test -f ./internal/testing/Dockerfile.c4ts ./internal/testing"))
	require.NoError(t, x("kind load --name=atc-test docker-image yokecd/c4ts:test"))

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
		CreateNamespaces: true,
		CreateCRDs:       true,
		Wait:             30 * time.Second,
		Poll:             time.Second,
	}

	require.NoError(t, commander.Takeoff(ctx, atcTakeoffParams))

	wasmcacheTakeoffParams := yoke.TakeoffParams{
		Release: "wasmcache",
		Flight: yoke.FlightParams{
			Path: "./test_output/backend.wasm",
			Input: strings.NewReader(`{
        "image": "yokecd/wasmcache:test",
        "replicas": 1
      }`),
			Namespace: "atc",
		},
		Wait: 30 * time.Second,
		Poll: time.Second,
	}

	require.NoError(t, commander.Takeoff(ctx, wasmcacheTakeoffParams))

	airwayTakeoffParams := yoke.TakeoffParams{
		Release: "backend-airway",
		Flight: yoke.FlightParams{
			Input: jsonReader(v1alpha1.Airway{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "backends.examples.com",
				},
				Spec: v1alpha1.AirwaySpec{
					WasmURL:          "http://wasmcache",
					FixDriftInterval: openapi.Duration(30 * time.Second),
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

	// TODO: remove eventually code once yoke has proper support for Airway readiness.
	EventuallyNoErrorf(
		t,
		func() error {
			return commander.Takeoff(ctx, yoke.TakeoffParams{
				Release: "c4ts",
				Flight: yoke.FlightParams{
					Input: jsonReader(backendv1.Backend{
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
			})
		},
		time.Second,
		30*time.Second,
		"failed to create backend resource",
	)

	EventuallyNoErrorf(
		t,
		func() error {
			pods, err := client.Clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{
				LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"test.app": "c4ts"},
				}),
			})
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
}

type xoptions struct {
	Env []string
	Dir string
}

func env(e ...string) XOpt {
	return func(opts *xoptions) {
		opts.Env = e
	}
}

func dir(d string) XOpt {
	return func(opts *xoptions) {
		opts.Dir = d
	}
}

type XOpt func(*xoptions)

func x(line string, opts ...XOpt) error {
	var options xoptions
	for _, apply := range opts {
		apply(&options)
	}

	args := regexp.MustCompile(`\s+`).Split(line, -1)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), options.Env...)
	cmd.Dir = options.Dir

	ansi.MakeStyle(ansi.FgCyan).Println(line)
	return cmd.Run()
}

func jsonReader(value any) io.Reader {
	data, err := json.Marshal(value)
	if err != nil {
		pr, pw := io.Pipe()
		pw.CloseWithError(err)
		return pr
	}
	return bytes.NewReader(data)
}

func EventuallyNoErrorf(t *testing.T, fn func() error, tick time.Duration, timeout time.Duration, msg string, args ...any) {
	t.Helper()

	var (
		ticker   = time.NewTimer(0)
		deadline = time.Now().Add(timeout)
	)

	for range ticker.C {
		err := fn()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			require.NoErrorf(t, err, msg, args...)
			return
		}
		ticker.Reset(tick)
	}
}
