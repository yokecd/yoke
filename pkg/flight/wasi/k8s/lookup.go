//go:build !wasip1

package k8s

import (
	"cmp"
	"context"
	"fmt"
	"sync"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
)

// func lookup(ptr wasm.Ptr, name, namespace, kind, apiversion wasm.String) wasm.Buffer {
// 	panic("mock lookup not implemented: should be used in the context of wasip1")
// }

var getClient = sync.OnceValues(func() (client *k8s.Client, err error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return k8s.NewClient(cfg, "default")
	}
	return k8s.NewClientFromKubeConfig(home.Kubeconfig)
})

func Lookup[T any](identifier ResourceIdentifier) (*T, error) {
	client, err := getClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes client: %w", err)
	}

	gv, err := schema.ParseGroupVersion(identifier.ApiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiVerion: %w", err)
	}

	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: identifier.Kind}, gv.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping: %w", err)
	}

	intf := func() dynamic.ResourceInterface {
		if mapping.Scope == meta.RESTScopeNamespace {
			return client.Dynamic.Resource(mapping.Resource).Namespace(cmp.Or(identifier.Namespace, client.DefaultNamespace))
		}
		return client.Dynamic.Resource(mapping.Resource)
	}()

	obj, err := intf.Get(context.Background(), identifier.Name, metav1.GetOptions{})
	if err != nil {
		switch {
		case kerrors.IsNotFound(err):
			return nil, ErrorNotFound(err.Error())
		case kerrors.IsUnauthorized(err):
			return nil, ErrorUnauthenticated(err.Error())
		case kerrors.IsForbidden(err):
			return nil, ErrorForbidden(err.Error())
		default:
			return nil, err
		}
	}

	var result T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &result); err != nil {
		return nil, fmt.Errorf("failed to convert to structured result: %w", err)
	}

	return &result, nil
}
