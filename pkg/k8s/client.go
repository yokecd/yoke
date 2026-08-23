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
func (client *Client) TypedInterface[T any, obj k8s.MetaObject[T]](resource schema.GroupVersionResource) TypedIntf[T] {
	return (*k8s.Client)(client).TypedInterface[T, obj](resource)
}

type WaitOptions = k8s.WaitOptions

// WaitForReady polls the resource until it is deemed to be ready.
func (client *Client) WaitForReady[T any, obj k8s.MetaObject[T]](ctx context.Context, resource *T, opts WaitOptions) error {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(resource)
	if err != nil {
		return err
	}
	return (*k8s.Client)(client).WaitForReady(ctx, &unstructured.Unstructured{Object: raw}, opts)
}
