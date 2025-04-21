package k8s

import (
	"context"
	"errors"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
)

// isReady checks for readiness of workload resources, namespaces, and CRDs
func (client Client) isReady(ctx context.Context, resource *unstructured.Unstructured) (bool, error) {
	gvk := resource.GroupVersionKind()

	switch gvk.Group {
	case "":
		switch gvk.Kind {
		case "Namespace":
			phase, _, _ := unstructured.NestedString(resource.Object, "status", "phase")
			return phase == "Active", nil
		case "Pod":
			return meetsConditions(resource, "Available"), nil
		case "Service":
			endpoints, err := client.Clientset.CoreV1().Endpoints(resource.GetNamespace()).Get(ctx, resource.GetName(), metav1.GetOptions{})
			if kerrors.IsNotFound(err) {
				err = nil
			}
			return endpoints != nil && len(endpoints.Subsets) > 0, err
		}
	case "apps":
		switch gvk.Kind {
		case "Deployment":
			return true &&
				meetsConditions(resource, "Available") &&
				equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas"), nil
		case "ReplicaSet", "StatefulSet":
			return equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas"), nil
		case "DaemonSet":
			return equalInts(
				resource,
				"currentNumberScheduled",
				"desiredNumberScheduled",
				"updatedNumberScheduled",
				"numberAvailable",
				"numberReady",
			), nil
		}
	case "batch":
		switch gvk.Kind {
		case "Job":
			if meetsConditions(resource, "Failed") {
				return false, errors.New("job has failed")
			}
			return meetsConditions(resource, "Complete"), nil
		}
	case "apiextensions.k8s.io":
		switch gvk.Kind {
		case "CustomResourceDefinition":
			return meetsConditions(resource, "Established"), nil
		}
	case "yoke.cd":
		switch gvk.Kind {
		case "Airway":
			status, _, _ := unstructured.NestedString(resource.Object, "status", "status")
			return status == "Ready", nil
		}
	}

	// if the resource is owned by an airway, it is an instance of that airway and so uses standard flight status.
	if _, ok := internal.Find(resource.GetOwnerReferences(), func(ref metav1.OwnerReference) bool {
		return ref.APIVersion == v1alpha1.APIVersion && ref.Kind == v1alpha1.KindAirway
	}); ok {
		status, _, _ := unstructured.NestedString(resource.Object, "status", "status")
		return status == "Ready", nil
	}

	return true, nil
}

func meetsConditions(resource *unstructured.Unstructured, keys ...string) bool {
	conditions, _, _ := unstructured.NestedSlice(resource.Object, "status", "conditions")

	trueConditions := map[string]bool{}
	for _, condition := range conditions {
		values, _ := condition.(map[string]any)
		cond, _ := values["type"].(string)
		if cond == "" {
			continue
		}
		trueConditions[cond] = values["status"] == "True"
	}

	for _, key := range keys {
		if !trueConditions[key] {
			return false
		}
	}

	return true
}

func equalInts(resource *unstructured.Unstructured, keys ...string) bool {
	if len(keys) == 0 {
		return true
	}

	values := []int64{}
	for _, key := range keys {
		value, _, _ := unstructured.NestedInt64(resource.Object, "status", key)
		values = append(values, value)
	}

	wanted := values[0]
	for _, value := range values[1:] {
		if value != wanted {
			return false
		}
	}

	return true
}
