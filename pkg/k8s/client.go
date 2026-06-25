package k8s

import (
	"cmp"
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/yokecd/yoke/internal/k8s"
)

type Client k8s.Client

func NewClient(kubecfg *rest.Config) (*Client, error) {
	kubecfg.Burst = cmp.Or(kubecfg.Burst, 300)
	kubecfg.QPS = cmp.Or(kubecfg.QPS, 50)
	client, err := k8s.NewClient(kubecfg, "")
	if err != nil {
		return nil, err
	}
	return (*Client)(client), nil
}

type TypedIntf[T any] = k8s.TypedIntf[T]

// TypedInterface returns a typed wrapper over the client-go dynamic client.
//
// TODO: once go1.27 is out and generic functions are added this should become a method of the standard client.
func TypedInterface[T any, obj k8s.MetaObject[T]](client *Client, resource schema.GroupVersionResource) TypedIntf[T] {
	return k8s.TypedInterface[T, obj]((*k8s.Client)(client), resource)
}

type WaitOptions = k8s.WaitOptions

// WaitForReady polls the resource until it is deemed to be ready.
//
// TODO: once go1.27 is out and generic methods are added this should become a method of the standard client.
func WaitForReady[T any, obj k8s.MetaObject[T]](ctx context.Context, client *Client, resource *T, opts WaitOptions) error {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(resource)
	if err != nil {
		return err
	}
	return (*k8s.Client)(client).WaitForReady(ctx, &unstructured.Unstructured{Object: raw}, opts)
}
