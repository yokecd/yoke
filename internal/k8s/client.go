package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type TypedIntf[T any] struct {
	intf dynamic.NamespaceableResourceInterface
	ns   string
}

func (c TypedIntf[T]) Namespace(ns string) TypedIntf[T] {
	c.ns = ns
	return c
}

func (c TypedIntf[T]) getIntf() dynamic.ResourceInterface {
	if c.ns == "" {
		return c.intf
	}
	return c.intf.Namespace(c.ns)
}

func (c TypedIntf[T]) Get(ctx context.Context, name string, options metav1.GetOptions) (*T, error) {
	obj, err := c.getIntf().Get(ctx, name, options)
	if err != nil {
		return nil, err
	}
	var result T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &result); err != nil {
		return nil, fmt.Errorf("failed to convert unstructerd value to typed api: %w", err)
	}
	return &result, nil
}

func (c TypedIntf[T]) Create(ctx context.Context, api *T, options metav1.CreateOptions) (*T, error) {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(api)
	if err != nil {
		return nil, fmt.Errorf("failed to convert typed api to unstructured object: %w", err)
	}
	obj, err := c.getIntf().Create(ctx, &unstructured.Unstructured{Object: raw}, options)
	if err != nil {
		return nil, err
	}
	result := *api
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &result); err != nil {
		return nil, fmt.Errorf("failed to convert unstructerd value to typed api: %w", err)
	}
	return &result, nil
}

func (c TypedIntf[T]) Update(ctx context.Context, api *T, options metav1.UpdateOptions) (*T, error) {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(api)
	if err != nil {
		return nil, fmt.Errorf("failed to convert typed api to unstructured object: %w", err)
	}
	obj, err := c.getIntf().Update(ctx, &unstructured.Unstructured{Object: raw}, options)
	if err != nil {
		return nil, err
	}
	result := *api
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &result); err != nil {
		return nil, fmt.Errorf("failed to convert unstructerd value to typed api: %w", err)
	}
	return &result, nil
}

func (c TypedIntf[T]) UpdateStatus(ctx context.Context, api *T, options metav1.UpdateOptions) (*T, error) {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(api)
	if err != nil {
		return nil, fmt.Errorf("failed to convert typed api to unstructured object: %w", err)
	}
	obj, err := c.getIntf().UpdateStatus(ctx, &unstructured.Unstructured{Object: raw}, options)
	if err != nil {
		return nil, err
	}
	result := *api
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &result); err != nil {
		return nil, fmt.Errorf("failed to convert unstructerd value to typed api: %w", err)
	}
	return &result, nil
}

func (c TypedIntf[T]) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	return c.getIntf().Delete(ctx, name, options)
}

func (c TypedIntf[T]) List(ctx context.Context, options metav1.ListOptions) ([]T, error) {
	obj, err := c.getIntf().List(ctx, options)
	if err != nil {
		return nil, err
	}
	var result []T
	for _, elem := range obj.Items {
		var value T
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(elem.Object, &value); err != nil {
			return nil, fmt.Errorf("failed to convert unstructerd value to typed api: %w", err)
		}
		result = append(result, value)
	}
	return result, nil
}

type MetaObject[T any] interface {
	*T
	metav1.Object
}

func TypedInterface[T any, obj MetaObject[T]](client *dynamic.DynamicClient, resource schema.GroupVersionResource) TypedIntf[T] {
	return TypedIntf[T]{
		intf: client.Resource(resource),
		ns:   "",
	}
}
