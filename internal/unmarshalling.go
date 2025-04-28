package internal

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func (stages *Stages) UnmarshalJSON(data []byte) error {
	var resource unstructured.Unstructured
	if err := json.Unmarshal(data, &resource); err == nil {
		*stages = Stages{Stage{&resource}}
		return nil
	}

	var resources Stage
	if err := json.Unmarshal(data, &resources); err == nil {
		*stages = Stages{resources}
		return nil
	}

	var multiStageResources []Stage
	if err := json.Unmarshal(data, &multiStageResources); err != nil {
		return err
	}

	*stages = Stages(multiStageResources)
	return nil
}

func (stages Stages) Flatten() []*unstructured.Unstructured {
	var result []*unstructured.Unstructured
	for _, stage := range stages {
		result = append(result, stage...)
	}
	return result
}
