package host

import (
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/internal"
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

type resourceTrackingKey struct{}

type TrackedResources struct {
	External *xsync.Set[string]
	Internal *xsync.Set[string]
}

func WithResourceTracking(ctx context.Context) context.Context {
	return context.WithValue(ctx, resourceTrackingKey{}, TrackedResources{
		External: &xsync.Set[string]{},
		Internal: &xsync.Set[string]{},
	})
}

func ExternalResources(ctx context.Context) []string {
	resources, ok := ctx.Value(resourceTrackingKey{}).(TrackedResources)
	if !ok {
		return nil
	}
	return slices.Collect(resources.External.All())
}

func trackExternalRef(ctx context.Context, ref string) {
	if resources, ok := ctx.Value(resourceTrackingKey{}).(TrackedResources); ok {
		resources.External.Add(ref)
	}
}

func InternalResources(ctx context.Context) *xsync.Set[string] {
	resources, ok := ctx.Value(resourceTrackingKey{}).(TrackedResources)
	if !ok {
		return nil
	}
	return resources.Internal
}

func trackInternalRef(ctx context.Context, ref string) {
	if resources, ok := ctx.Value(resourceTrackingKey{}).(TrackedResources); ok {
		resources.Internal.Add(ref)
	}
}

type releaseTrackingKey struct{}

type TrackedRelease struct {
	Candidate *xsync.Set[string]
	Release   *xsync.Set[string]
}

func WithReleaseTracking(ctx context.Context) context.Context {
	return context.WithValue(ctx, releaseTrackingKey{}, TrackedRelease{
		Candidate: &xsync.Set[string]{},
		Release:   &xsync.Set[string]{},
	})
}

func CandidateResources(ctx context.Context) *xsync.Set[string] {
	resources, ok := ctx.Value(releaseTrackingKey{}).(TrackedRelease)
	if !ok {
		return nil
	}
	return resources.Candidate
}

func trackCandidateRef(ctx context.Context, ref string) {
	if resources, ok := ctx.Value(releaseTrackingKey{}).(TrackedRelease); ok {
		resources.Candidate.Add(ref)
	}
}

func ReleaseResources(ctx context.Context) *xsync.Set[string] {
	resources, ok := ctx.Value(releaseTrackingKey{}).(TrackedRelease)
	if !ok {
		return nil
	}
	return resources.Release
}

func SetReleaseResources(ctx context.Context, resources []*unstructured.Unstructured) {
	if tracked, ok := ctx.Value(releaseTrackingKey{}).(TrackedRelease); ok {
		for _, resource := range resources {
			tracked.Release.Add(internal.ResourceRef(resource))
		}
	}
}
