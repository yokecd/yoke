package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yokecd/yoke/internal"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAddSyncWaveAnnotations(t *testing.T) {
	cases := []struct {
		Name     string
		Input    internal.Stages
		Expected internal.Stages
	}{
		{
			Name: "single stage",
			Input: internal.Stages{internal.Stage{
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "a", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {}, "name": "b", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"foo": "bar"}, "name": "c", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "99"}, "name": "c", "namespace": "default"}}`),
			}},
			Expected: internal.Stages{internal.Stage{
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "a", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {}, "name": "b", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"foo": "bar"}, "name": "c", "namespace": "default"}}`),
				mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "99"}, "name": "c", "namespace": "default"}}`),
			}},
		}, {
			Name: "multi stage",
			Input: internal.Stages{
				{
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "a", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {}, "name": "b", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"foo": "bar"}, "name": "c", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "99"}, "name": "c", "namespace": "default"}}`),
				},
			},
			Expected: internal.Stages{
				{
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "0"}, "name": "a", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "1"}, "name": "b", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "2", "foo": "bar"}, "name": "c", "namespace": "default"}}`),
				}, {
					mustUnstructuredJSON(`{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"annotations": {"argocd.argoproj.io/sync-wave": "3"}, "name": "c", "namespace": "default"}}`),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			expected, err := json.MarshalIndent(tc.Expected.Flatten(), "", "  ")
			if err != nil {
				t.Error(err)
			}
			addSyncWaveAnnotations(tc.Input)
			actual, err := json.MarshalIndent(tc.Input.Flatten(), "", "  ")
			if err != nil {
				t.Error(err)
			}
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func mustUnstructuredJSON(j string) *unstructured.Unstructured {
	resource := &unstructured.Unstructured{}
	if err := json.Unmarshal([]byte(j), resource); err != nil {
		panic(err)
	}
	return resource
}
