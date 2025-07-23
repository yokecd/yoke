package wasi

import (
	"cmp"
	"context"
	"errors"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
)

type HostLookupResourceFunc func(ctx context.Context, name, namespace, kind, apiVersion string) (*unstructured.Unstructured, error)

var ErrFeatureNotGranted = errors.New("feature not granted")

func HostLookupResource(client *k8s.Client) HostLookupResourceFunc {
	return func(ctx context.Context, name, namespace, kind, apiVersion string) (*unstructured.Unstructured, error) {
		clusterAccess := clusterAccessEnabled(ctx)
		if !clusterAccess.Enabled {
			return nil, ErrFeatureNotGranted
		}

		gv, err := schema.ParseGroupVersion(apiVersion)
		if err != nil {
			return nil, err
		}

		mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version)
		if err != nil {
			return nil, err
		}

		intf := func() dynamic.ResourceInterface {
			intf := client.Dynamic.Resource(mapping.Resource)
			if mapping.Scope == meta.RESTScopeNamespace {
				return intf.Namespace(cmp.Or(namespace, "default"))
			}
			return intf
		}()

		resource, err := intf.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		for _, matcher := range clusterAccess.ResourceMatchers {
			if internal.MatchResource(resource, matcher) {
				return resource, nil
			}
		}

		if internal.GetOwner(resource) != getOwner(ctx) {
			return nil, kerrors.NewForbidden(schema.GroupResource{}, "", errors.New("cannot access resource outside of target release ownership"))
		}

		return resource, nil
	}
}

type ownerKey struct{}

func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

func getOwner(ctx context.Context) string {
	value, _ := ctx.Value(ownerKey{}).(string)
	return value
}

type clusterAccessKey struct{}

type ClusterAccessParams struct {
	Enabled          bool
	ResourceMatchers []string
}

func WithClusterAccess(ctx context.Context, access ClusterAccessParams) context.Context {
	return context.WithValue(ctx, clusterAccessKey{}, access)
}

func clusterAccessEnabled(ctx context.Context) ClusterAccessParams {
	value, _ := ctx.Value(clusterAccessKey{}).(ClusterAccessParams)
	return value
}
