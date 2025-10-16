package host

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tetratelabs/wazero/api"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi"
	"github.com/yokecd/yoke/internal/wasm"
	"github.com/yokecd/yoke/internal/xsync"
)

var ErrFeatureNotGranted = errors.New("feature not granted")

func BuildFunctionMap(client *k8s.Client) map[string]any {
	lookup := HostLookupResource(client)
	restMapping := HostDiscoverMapping(client)

	errHandler := func(ctx context.Context, module api.Module, stateRef wasm.Ptr, err error) wasm.Buffer {
		errState := func() wasm.State {
			switch {
			case errors.Is(err, ErrFeatureNotGranted):
				return wasm.StateFeatureNotGranted
			case kerrors.IsNotFound(err):
				return wasm.StateNotFound
			case kerrors.IsForbidden(err):
				return wasm.StateForbidden
			case kerrors.IsUnauthorized(err):
				return wasm.StateUnauthenticated
			default:
				return wasm.StateError
			}
		}()
		return wasi.Error(ctx, module, stateRef, errState, err.Error())
	}

	return map[string]any{
		"k8s_lookup": func(ctx context.Context, module api.Module, stateRef wasm.Ptr, name, namespace, kind, apiVersion wasm.String) wasm.Buffer {
			resource, err := lookup(
				ctx,
				wasi.LoadString(module, name),
				wasi.LoadString(module, namespace),
				wasi.LoadString(module, kind),
				wasi.LoadString(module, apiVersion),
			)
			if err != nil {
				return errHandler(ctx, module, stateRef, err)
			}
			return wasi.MallocJSON(ctx, module, stateRef, resource)
		},

		"k8s_rest_mapping": func(ctx context.Context, module api.Module, stateRef wasm.Ptr, groupOrAPIVersion, kind wasm.String) wasm.Buffer {
			mapping, err := restMapping(ctx, wasi.LoadString(module, groupOrAPIVersion), wasi.LoadString(module, kind))
			if err != nil {
				return errHandler(ctx, module, stateRef, err)
			}
			return wasi.MallocJSON(ctx, module, stateRef, mapping)
		},
	}
}

type HostLookupResourceFunc func(ctx context.Context, name, namespace, kind, apiVersion string) (*unstructured.Unstructured, error)

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

		gk := schema.GroupKind{Group: gv.Group, Kind: kind}

		mapping, err := client.Mapper.RESTMapping(gk, gv.Version)
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
			resourceID := fmt.Sprintf("%s/%s:%s", namespace, gk.String(), name)
			if kerrors.IsNotFound(err) && slices.ContainsFunc(clusterAccess.ResourceMatchers, func(matcher string) bool {
				return internal.MatcherContains(matcher, resourceID)
			}) {
				if externalResources, ok := ctx.Value(externalResourceTrackingKey{}).(*xsync.Set[string]); ok {
					externalResources.Add(resourceID)
				}
			}
			return nil, err
		}

		if slices.ContainsFunc(clusterAccess.ResourceMatchers, func(matcher string) bool {
			return internal.MatchResource(resource, matcher)
		}) {
			if externalResources, ok := ctx.Value(externalResourceTrackingKey{}).(*xsync.Set[string]); ok {
				externalResources.Add(internal.ResourceString(resource))
			}
			return resource, nil
		}

		if internal.GetOwner(resource) != getOwner(ctx) {
			return nil, kerrors.NewForbidden(schema.GroupResource{}, "", errors.New("cannot access resource outside of target release ownership"))
		}

		return resource, nil
	}
}

type RestMapping struct {
	Group      string
	Version    string
	Kind       string
	Resource   string
	Namespaced bool
}

type HostDiscoverMappingFunc func(ctx context.Context, group, kind string) (*RestMapping, error)

func HostDiscoverMapping(client *k8s.Client) HostDiscoverMappingFunc {
	return func(ctx context.Context, groupOrAPIVersion, kind string) (*RestMapping, error) {
		clusterAccess := clusterAccessEnabled(ctx)
		if !clusterAccess.Enabled {
			return nil, ErrFeatureNotGranted
		}

		group, version, _ := strings.Cut(groupOrAPIVersion, "/")

		var versions []string
		if version != "" {
			versions = append(versions, version)
		}

		mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind}, versions...)
		if err != nil {
			return nil, err
		}

		return &RestMapping{
			Group:      mapping.GroupVersionKind.Group,
			Version:    mapping.GroupVersionKind.Version,
			Kind:       mapping.GroupVersionKind.Kind,
			Resource:   mapping.Resource.Resource,
			Namespaced: mapping.Scope.Name() == meta.RESTScopeNameNamespace,
		}, nil
	}
}
