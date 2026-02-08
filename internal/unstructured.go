package internal

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func UnstructuredObject[T any](value any) (T, error) {
	data, err := json.Marshal(value)
	if err != nil {
		var zero T
		return zero, err
	}
	var result T
	err = json.Unmarshal(data, &result)
	return result, err
}

func ToUnstructured(value any) (*unstructured.Unstructured, error) {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(value)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: obj}, nil
}

func MustUnstructuredObject[T any](value any) T {
	result, err := UnstructuredObject[T](value)
	if err != nil {
		panic(err)
	}
	return result
}

func ResourcesAreEqual(a, b *unstructured.Unstructured) bool {
	if (a == nil) || (b == nil) {
		return false
	}

	dropKeys := [][]string{
		{"metadata", "generation"},
		{"metadata", "resourceVersion"},
		{"metadata", "managedFields"},
		{"status"},
	}

	return reflect.DeepEqual(
		DropProperties(a, dropKeys).Object,
		DropProperties(b, dropKeys).Object,
	)
}

func ResourcesAreEqualWithStatus(a, b *unstructured.Unstructured) bool {
	if (a == nil) || (b == nil) {
		return false
	}

	dropKeys := [][]string{
		{"metadata", "generation"},
		{"metadata", "resourceVersion"},
		{"metadata", "managedFields"},
	}

	return reflect.DeepEqual(
		DropProperties(a, dropKeys).Object,
		DropProperties(b, dropKeys).Object,
	)
}

func DropProperties(resource *unstructured.Unstructured, props [][]string) *unstructured.Unstructured {
	if resource == nil {
		return nil
	}

	resource = resource.DeepCopy()

	for _, keys := range props {
		unstructured.RemoveNestedField(resource.Object, keys...)
	}

	return resource
}

// RemoveAdditions removes fields from actual that are not in expected.
// it removes the additional properties in place and returns "actual" back.
// Values passed to removeAdditions are expected to be generic json compliant structures:
// map[string]any, []any, or scalars.
func RemoveAdditions[T any](expected, actual T) T {
	// Check if we can access the types safely
	if !reflect.ValueOf(expected).IsValid() || !reflect.ValueOf(actual).IsValid() || reflect.ValueOf(actual).Type() != reflect.ValueOf(expected).Type() {
		return actual
	}

	switch a := any(actual).(type) {
	case map[string]any:
		e := any(expected).(map[string]any)
		for key := range a {
			if _, ok := e[key]; !ok {
				delete(a, key)
				continue
			}
			a[key] = RemoveAdditions(e[key], a[key])
		}
	case []any:
		e := any(expected).([]any)
		for i := range min(len(a), len(e)) {
			a[i] = RemoveAdditions(e[i], a[i])
		}
	}

	return actual
}

func GetFlightConditions(resource *unstructured.Unstructured) []metav1.Condition {
	if resource == nil {
		return nil
	}

	rawConditions, _, _ := unstructured.NestedFieldNoCopy(resource.Object, "status", "conditions")

	data, _ := json.Marshal(rawConditions)

	var conditions []metav1.Condition
	json.Unmarshal(data, &conditions)

	return conditions
}

func GetFlightReadyCondition(resource *unstructured.Unstructured) *metav1.Condition {
	cond, ok := Find(GetFlightConditions(resource), func(cond metav1.Condition) bool {
		return cond.Type == "Ready"
	})
	if !ok {
		return nil
	}
	return &cond
}

func IsNamespace(resource *unstructured.Unstructured) bool {
	return resource != nil && resource.GroupVersionKind().GroupKind() == schema.GroupKind{Kind: "Namespace"}
}

func IsCRD(resource *unstructured.Unstructured) bool {
	return resource != nil && resource.GroupVersionKind().GroupKind() == schema.GroupKind{
		Group: "apiextensions.k8s.io",
		Kind:  "CustomResourceDefinition",
	}
}

func ResourceRef(resource *unstructured.Unstructured) string {
	return fmt.Sprintf("%s/%s:%s", resource.GetNamespace(), resource.GroupVersionKind().GroupKind().String(), resource.GetName())
}

func ParseRef(ref string) (ns, gk, name string) {
	if ref == "" {
		return
	}
	ns, rest, ok := strings.Cut(ref, "/")
	if !ok {
		rest = ns
		ns = ""
	}
	gk, name, _ = strings.Cut(rest, ":")
	return
}
