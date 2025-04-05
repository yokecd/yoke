package internal

import (
	"encoding/json"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func UnstructuredObject(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}

func ToUnstructured(value any) (*unstructured.Unstructured, error) {
	m, err := UnstructuredObject(value)
	return &unstructured.Unstructured{Object: m}, err
}

func MustUnstructuredObject(value any) map[string]any {
	result, _ := UnstructuredObject(value)
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
