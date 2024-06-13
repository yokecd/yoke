package k8s

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// isReady checks for readiness of workload resources, namespaces, and CRDs
func isReady(_ context.Context, resource *unstructured.Unstructured) bool {
	gvk := resource.GroupVersionKind()

	switch gvk.Group {
	case "":
		switch gvk.Kind {
		case "Namespace":
			phase, _, _ := unstructured.NestedString(resource.Object, "status", "phase")
			return phase == "Active"
		case "Pod":
			return meetsConditions(resource, "Available")
		}
	case "apps":
		switch gvk.Kind {
		case "Deployment":
			return true &&
				meetsConditions(resource, "Available") &&
				equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas")
		case "ReplicaSet", "StatefulSet":
			return equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas")
		case "DaemonSet":
			return equalInts(
				resource,
				"currentNumberScheduled",
				"desiredNumberScheduled",
				"updatedNumberScheduled",
				"numberAvailable",
				"numberReady",
			)
		}
	case "apiextensions.k8s.io":
		switch gvk.Kind {
		case "CustomResourceDefinition":
			return meetsConditions(resource, "Established")
		}
	}

	return true
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
