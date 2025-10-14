package host

import (
	"context"
	"slices"

	"github.com/yokecd/yoke/internal/xsync"
)

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

type externalResourceTrackingKey struct{}

func WithExternalResourceTracking(ctx context.Context) context.Context {
	return context.WithValue(ctx, externalResourceTrackingKey{}, new(xsync.Set[string]))
}

func TrackedResources(ctx context.Context) []string {
	resources, ok := ctx.Value(externalResourceTrackingKey{}).(*xsync.Set[string])
	if !ok {
		return nil
	}
	return slices.Collect(resources.All())
}
