package internal

import (
	"encoding/json"

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
