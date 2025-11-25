package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
)

func TestFlightInstance(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	flightIntf := k8s.TypedInterface[v1alpha1.Flight](client.Dynamic, v1alpha1.FlightGVR()).Namespace("default")

	toJSONString := func(t *testing.T, value any) string {
		var buffer bytes.Buffer
		require.NoError(t, json.NewEncoder(&buffer).Encode(value))
		return buffer.String()
	}

	flight, err := flightIntf.Create(
		context.Background(),
		&v1alpha1.Flight{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.KindFlight,
				APIVersion: v1alpha1.AirwayGVR().GroupVersion().Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "basic",
			},
			Spec: v1alpha1.FlightSpec{
				WasmURL: "http://wasmcache/basic.wasm",
				Input:   toJSONString(t, map[string]string{"hello": "world"}),
			},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	cmIntf := client.Clientset.CoreV1().ConfigMaps("default")

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			cm, err := cmIntf.Get(context.Background(), flight.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected configmap: %w", err)
			}
			if hello := cm.Data["hello"]; hello != "world" {
				return fmt.Errorf("expected configmap data hello to be world but got: %s", hello)
			}
			return nil
		},
		time.Second,
		10*time.Second,
		"flight resources were not created as expected",
	)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		flight, err = flightIntf.Get(context.Background(), flight.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		flight.Spec.Input = toJSONString(t, map[string]string{"hello": "42"})

		flight, err = flightIntf.Update(context.Background(), flight, metav1.UpdateOptions{})
		return err
	})
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			cm, err := cmIntf.Get(context.Background(), flight.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get expected configmap: %w", err)
			}
			if hello := cm.Data["hello"]; hello != "42" {
				return fmt.Errorf("expected configmap data hello to be 42 but got: %s", hello)
			}
			return nil
		},
		time.Second,
		10*time.Second,
		"flight resources were not updated as expected",
	)

	require.NoError(t, flightIntf.Delete(context.Background(), flight.Name, metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if _, err := cmIntf.Get(context.Background(), flight.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected config map not to be found but got error: %w", err)
			}
			if _, err := flightIntf.Get(context.Background(), flight.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected flight not to be found but got error: %w", err)
			}
			return nil
		},
		time.Second,
		10*time.Second,
		"flight did not delete as expected",
	)
}

func TestFlightCrossNamespace(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	flightIntf := k8s.TypedInterface[v1alpha1.Flight](client.Dynamic, v1alpha1.FlightGVR()).Namespace("default")

	_, err = flightIntf.Create(
		context.Background(),
		&v1alpha1.Flight{
			ObjectMeta: metav1.ObjectMeta{Name: "crossname"},
			Spec:       v1alpha1.FlightSpec{WasmURL: "http://wasmcache/crossnamespace.wasm"},
		},
		metav1.CreateOptions{},
	)
	require.ErrorContains(t, err, "Multiple namespaces detected (if desired enable multinamespace releases)")

	clusterFlightIntf := k8s.TypedInterface[v1alpha1.ClusterFlight](client.Dynamic, v1alpha1.ClusterFlightGVR())

	// Crossnamespace depends on "foo" and "bar"
	for _, ns := range []string{"foo", "bar"} {
		require.NoError(t, client.EnsureNamespace(context.Background(), ns))
		defer func() {
			require.NoError(t, client.Clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}))
		}()
	}

	clusterFlight, err := clusterFlightIntf.Create(
		context.Background(),
		&v1alpha1.ClusterFlight{
			ObjectMeta: metav1.ObjectMeta{Name: "crossname"},
			Spec:       v1alpha1.FlightSpec{WasmURL: "http://wasmcache/crossnamespace.wasm"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			flight, err := clusterFlightIntf.Get(context.Background(), clusterFlight.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			ready := meta.FindStatusCondition(flight.Status.Conditions, "Ready")
			if ready == nil {
				return fmt.Errorf("expected a status condition to be present but none was")
			}
			if ready.Status != metav1.ConditionTrue {
				return fmt.Errorf("expected ready condition to be true but got false")
			}
			return nil
		},
		time.Second,
		30*time.Second,
		"expected cluster flight to succeed",
	)

	require.NoError(t, clusterFlightIntf.Delete(context.Background(), clusterFlight.Name, metav1.DeleteOptions{}))

	testutils.EventuallyNoErrorf(
		t,
		func() error {
			if _, err := clusterFlightIntf.Get(context.Background(), clusterFlight.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				return fmt.Errorf("expected flight not to be found but got error: %w", err)
			}
			return nil
		},
		time.Second,
		10*time.Second,
		"cluster flight did not delete as expected",
	)
}
