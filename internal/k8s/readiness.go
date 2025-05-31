package k8s

import (
	"context"
	"errors"

	discoveryv1 "k8s.io/api/discovery/v1"
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
			endpoints, err := client.Clientset.DiscoveryV1().EndpointSlices(resource.GetNamespace()).List(ctx, metav1.ListOptions{
				LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{discoveryv1.LabelServiceName: resource.GetName()},
				}),
			})
			return endpoints != nil && len(endpoints.Items) > 0, err
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
			return FlightIsReady(resource), nil
		}
	}

	// if the resource is owned by an airway, it is an instance of that airway and so uses standard flight status.
	if _, ok := internal.Find(resource.GetOwnerReferences(), func(ref metav1.OwnerReference) bool {
		return ref.APIVersion == v1alpha1.APIVersion && ref.Kind == v1alpha1.KindAirway
	}); ok {
		return FlightIsReady(resource), nil
	}

	return true, nil
}

func FlightIsReady(resource *unstructured.Unstructured) bool {
	cond := internal.GetFlightReadyCondition(resource)
	return cond != nil && cond.Type == "Ready" && cond.Status == metav1.ConditionTrue && cond.ObservedGeneration == resource.GetGeneration()
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
