package internal

import (
	"bytes"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type List[T any] []T

func (value *List[T]) UnmarshalJSON(data []byte) error {
	var single T
	if err := json.Unmarshal(data, &single); err == nil {
		*value = []T{single}
		return nil
	}

	var many []T
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}

	*value = many
	return nil
}

type (
	Stage  []*unstructured.Unstructured
	Stages []Stage
)

func ParseStages(data []byte) (Stages, error) {
	decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data))

	var singleStage Stage
	for {
		var resource unstructured.Unstructured
		if err := decoder.Decode(&resource); err != nil {
			break
		}
		singleStage = append(singleStage, &resource)
	}

	if len(singleStage) > 0 {
		return Stages{singleStage}, nil
	}

	var resources Stage
	if err := yaml.Unmarshal(data, &resources); err == nil {
		return Stages{resources}, nil
	}

	var multiStageResources []Stage
	if err := yaml.Unmarshal(data, &multiStageResources); err != nil {
		return nil, fmt.Errorf("input must be resource, list of resources, or list of list of resources")
	}

	return Stages(multiStageResources), nil
}

func (stages Stages) Flatten() []*unstructured.Unstructured {
	var result []*unstructured.Unstructured
	for _, stage := range stages {
		result = append(result, stage...)
	}
	return result
}
