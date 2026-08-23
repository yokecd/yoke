package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/testutils"
	"github.com/yokecd/yoke/pkg/openapi"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestReadinessLoading(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	configmaps := []corev1.ConfigMap{
		{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:   "custom-readiness-a",
				Labels: map[string]string{"resource.yoke.cd/readiness": "conditions"},
			},
			Data: map[string]string{
				"CustomKind.custom.group": "",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:   "custom-readiness-b",
				Labels: map[string]string{"resource.yoke.cd/readiness": "lua"},
			},
			Data: map[string]string{
				"CustomKind.custom.group": "",
				"OtherKind.custom.group":  "",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:   "custom-readiness-c",
				Labels: map[string]string{"resource.yoke.cd/readiness": "invalid"},
			},
			Data: map[string]string{
				"a.b.c": "",
				"d.e.f": "",
			},
		},
	}

	cmIntf := client.Clientset.CoreV1().ConfigMaps("default")

	t.Run("on demand", func(t *testing.T) {
		readiness, err := client.LoadCustomReadinessFuncs(t.Context())
		require.NoError(t, err)
		require.Equal(t, 0, readiness.Len())

		for _, cm := range configmaps {
			_, err := cmIntf.Create(t.Context(), &cm, metav1.CreateOptions{})
			require.NoError(t, err)
		}

		readiness, err = client.LoadCustomReadinessFuncs(t.Context())
		require.NoError(t, err)

		// There was an overlap between the two configmaps so there should only be 2.
		require.Equal(
			t,
			[]schema.GroupKind{
				{Group: "custom.group", Kind: "CustomKind"},
				{Group: "custom.group", Kind: "OtherKind"},
			},
			slices.SortedFunc(readiness.Keys(), func(a, b schema.GroupKind) int {
				return strings.Compare(a.String(), b.String())
			}),
		)

		for _, cm := range configmaps {
			require.NoError(t, cmIntf.Delete(t.Context(), cm.Name, metav1.DeleteOptions{}))
		}

		readiness, err = client.LoadCustomReadinessFuncs(t.Context())
		require.NoError(t, err)
		require.Equal(t, 0, readiness.Len())
	})

	t.Run("watching", func(t *testing.T) {
		readiness, stop, err := client.WatchCustomReadiness(t.Context())
		require.NoError(t, err)
		defer stop()

		require.Equal(t, 0, readiness.Len())

		_, err = cmIntf.Create(t.Context(), &configmaps[0], metav1.CreateOptions{})
		require.NoError(t, err)

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if !reflect.DeepEqual(
					[]schema.GroupKind{{Group: "custom.group", Kind: "CustomKind"}},
					slices.SortedFunc(readiness.Keys(), func(a, b schema.GroupKind) int {
						return strings.Compare(a.String(), b.String())
					}),
				) {
					return fmt.Errorf("unexpected readiness state")
				}
				return nil
			},
			25*time.Millisecond,
			time.Second,
			"failed to see readiness updated",
		)

		_, err = cmIntf.Create(t.Context(), &configmaps[1], metav1.CreateOptions{})
		require.NoError(t, err)

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if !reflect.DeepEqual(
					[]schema.GroupKind{
						{Group: "custom.group", Kind: "CustomKind"},
						{Group: "custom.group", Kind: "OtherKind"},
					},
					slices.SortedFunc(readiness.Keys(), func(a, b schema.GroupKind) int {
						return strings.Compare(a.String(), b.String())
					}),
				) {
					return fmt.Errorf("unexpected readiness state")
				}
				return nil
			},
			25*time.Millisecond,
			time.Second,
			"failed to see readiness updated",
		)

		for _, cm := range configmaps[:2] {
			require.NoError(t, cmIntf.Delete(t.Context(), cm.Name, metav1.DeleteOptions{}))
		}

		testutils.EventuallyNoErrorf(
			t,
			func() error {
				if count := readiness.Len(); count != 0 {
					return fmt.Errorf("expected readiness state to be empty but got: %d items", count)
				}
				return nil
			},
			25*time.Millisecond,
			time.Second,
			"failed to see readiness updated",
		)
	})

	t.Run("during takeoff", func(t *testing.T) {
		crdIntf := client.TypedInterface[apiextv1.CustomResourceDefinition](schema.GroupVersionResource{
			Group:    apiextv1.SchemeGroupVersion.Group,
			Version:  apiextv1.SchemeGroupVersion.Version,
			Resource: "customresourcedefinitions",
		})

		type TestStatus struct {
			Conditions []metav1.Condition `json:"conditions,omitempty"`
			Ready      bool               `json:"ready,omitempty"`
		}

		type TestSpec struct {
			Value int `json:"value"`
		}

		type TestResource struct {
			metav1.TypeMeta
			metav1.ObjectMeta `json:"metadata"`
			Spec              TestSpec   `json:"spec"`
			Status            TestStatus `json:"status,omitzero"`
		}

		crd, err := crdIntf.Create(
			t.Context(),
			&apiextv1.CustomResourceDefinition{
				TypeMeta:   metav1.TypeMeta{APIVersion: apiextv1.SchemeGroupVersion.Identifier(), Kind: "CustomResourceDefinition"},
				ObjectMeta: metav1.ObjectMeta{Name: "tests.yoke.cd"},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Group: "yoke.cd",
					Names: apiextv1.CustomResourceDefinitionNames{
						Kind:     "Test",
						Plural:   "tests",
						Singular: "test",
						ListKind: "TestList",
					},
					Scope: apiextv1.NamespaceScoped,
					Versions: []apiextv1.CustomResourceDefinitionVersion{
						{
							Name:         "v1",
							Storage:      true,
							Served:       true,
							Schema:       &apiextv1.CustomResourceValidation{OpenAPIV3Schema: openapi.SchemaFor[TestResource]()},
							Subresources: &apiextv1.CustomResourceSubresources{Status: &apiextv1.CustomResourceSubresourceStatus{}},
						},
					},
				},
			},
			metav1.CreateOptions{},
		)
		require.NoError(t, err)

		defer func() {
			require.NoError(t, crdIntf.Delete(t.Context(), crd.Name, metav1.DeleteOptions{}))
		}()

		require.NoError(t, client.WaitForReady(t.Context(), internal.MustUnstructured(crd), k8s.WaitOptions{
			Timeout:  10 * time.Second,
			Interval: time.Second / 2,
		}))

		commander := yoke.FromK8Client(client)

		conditions, err := cmIntf.Create(
			t.Context(),
			&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-readiness-conditions",
					Labels: map[string]string{"resource.yoke.cd/readiness": "conditions"},
				},
				Data: map[string]string{"Test.yoke.cd": "Ready"},
			},
			metav1.CreateOptions{},
		)
		require.NoError(t, err)

		lua, err := cmIntf.Create(
			t.Context(),
			&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-readiness-lua",
					Labels: map[string]string{"resource.yoke.cd/readiness": "lua"},
				},
				Data: map[string]string{"_.yoke.cd": "return function(resource)\n  return resource.status.ready\nend\n"},
			},
			metav1.CreateOptions{},
		)
		require.NoError(t, err)

		defer func() {
			for _, cm := range []*corev1.ConfigMap{conditions, lua} {
				if err := cmIntf.Delete(t.Context(), cm.Name, metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
					require.NoError(t, err)
				}
			}
		}()

		values := make(chan int)
		var done atomic.Bool

		ctx := internal.WithDebugFlag(t.Context(), new(true))

		go func() {
			for value := range values {
				func() {
					done.Store(false)
					defer done.Store(true)
					if err := commander.Takeoff(ctx, yoke.TakeoffParams{
						Release:             "foobar",
						Namespace:           "default",
						LoadCustomReadiness: true,
						Wait:                5 * time.Second,
						Poll:                250 * time.Millisecond,
						Flight: yoke.FlightParams{
							Input: internal.JSONReader(TestResource{
								TypeMeta:   metav1.TypeMeta{Kind: "Test", APIVersion: "yoke.cd/v1"},
								ObjectMeta: metav1.ObjectMeta{Name: "test"},
								Spec:       TestSpec{value},
							}),
						},
					}); err != nil {
						t.Log(err)
						return
					}
				}()
			}
		}()

		values <- 1

		eventuallyNotDone := func() {
			testutils.EventuallyNoErrorf(
				t,
				func() error {
					if done.Load() {
						return fmt.Errorf("done and shouln't be!")
					}
					return nil
				},
				50*time.Millisecond,
				2*time.Second,
				"expected release to not be done releasing.",
			)
		}

		// Wait some arbitrary amount of time. Long enough that we should expect the TestResource to be applied and for yoke to think it should be ready
		// were it not for its custom readiness definition.
		time.Sleep(200 * time.Millisecond)
		eventuallyNotDone()

		testIntf := client.TypedInterface[TestResource](schema.GroupVersionResource{Group: "yoke.cd", Version: "v1", Resource: "tests"}).Namespace("default")

		test, err := testIntf.Get(t.Context(), "test", metav1.GetOptions{})
		require.NoError(t, err)

		test.Status.Conditions = []metav1.Condition{{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: test.GetGeneration(),
			LastTransitionTime: metav1.Now(),
		}}

		test, err = testIntf.ApplyStatus(t.Context(), test, metav1.ApplyOptions{FieldManager: "readiness-test"})
		require.NoError(t, err)

		eventuallyDone := func() {
			testutils.EventuallyNoErrorf(
				t,
				func() error {
					if !done.Load() {
						return fmt.Errorf("not done yet.")
					}
					return nil
				},
				50*time.Millisecond,
				2*time.Second,
				"expected release to become ready but did not!",
			)
		}

		eventuallyDone()

		require.NoError(t, cmIntf.Delete(t.Context(), "test-readiness-conditions", metav1.DeleteOptions{}))

		values <- 2

		// give it a chance to "hang"
		time.Sleep(250 * time.Millisecond)
		eventuallyNotDone()

		test.Status.Ready = true

		_, err = testIntf.ApplyStatus(t.Context(), test, metav1.ApplyOptions{FieldManager: "readiness-test"})
		require.NoError(t, err)

		eventuallyDone()
	})
}
