package k8s

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/yoke/internal"
)

const (
	ResourceReleaseMapping = "yoke-resource-release-mapping"
	NSKubeSystem           = "kube-system"
	yoke                   = "yoke"
)

func yokePrefix(release string) string { return yoke + "-" + release }

type Client struct {
	dynamic   *dynamic.DynamicClient
	clientset *kubernetes.Clientset
	meta      metadata.Interface
	mapper    *restmapper.DeferredDiscoveryRESTMapper
}

func NewClientFromKubeConfig(path string) (*Client, error) {
	restcfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("failed to build k8 config: %w", err)
	}
	restcfg.Burst = cmp.Or(restcfg.Burst, 300)
	restcfg.QPS = cmp.Or(restcfg.QPS, 50)
	return NewClient(restcfg)
}

func NewClient(cfg *rest.Config) (*Client, error) {
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client component: %w", err)
	}

	meta, err := metadata.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata client component: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8 clientset: %w", err)
	}

	return &Client{
		dynamic:   dynamicClient,
		clientset: clientset,
		meta:      meta,
		mapper:    restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(clientset.DiscoveryClient)),
	}, nil
}

type ApplyResourcesOpts struct {
	SkipDryRun     bool
	ForceConflicts bool
}

func (client Client) ApplyResources(ctx context.Context, resources []*unstructured.Unstructured, opts ApplyResourcesOpts) error {
	defer internal.DebugTimer(ctx, "apply resources")()

	if !opts.SkipDryRun {
		dryOpts := ApplyOpts{DryRun: true}
		if err := xerr.MultiErrOrderedFrom("dry run", client.applyMany(ctx, resources, dryOpts)...); err != nil {
			return err
		}
	}

	applyOpts := ApplyOpts{DryRun: false, ForceConflicts: opts.ForceConflicts}

	return xerr.MultiErrOrderedFrom("", client.applyMany(ctx, resources, applyOpts)...)
}

func (client Client) applyMany(ctx context.Context, resources []*unstructured.Unstructured, opts ApplyOpts) []error {
	var wg sync.WaitGroup
	wg.Add(len(resources))

	errs := make([]error, len(resources))
	semaphore := make(chan struct{}, runtime.NumCPU())

	for i, resource := range resources {
		go func() {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			err := client.ApplyResource(ctx, resource, opts)
			if err != nil {
				err = fmt.Errorf("%s: %w", internal.Canonical(resource), err)
			}
			errs[i] = err
		}()
	}

	wg.Wait()

	return errs
}

type ApplyOpts struct {
	DryRun         bool
	ForceConflicts bool
}

func (client Client) ApplyResource(ctx context.Context, resource *unstructured.Unstructured, opts ApplyOpts) error {
	defer internal.DebugTimer(
		ctx,
		fmt.Sprintf(
			"%sapply resource %s/%s",
			func() string {
				if opts.DryRun {
					return "dry "
				}
				return ""
			}(),
			resource.GetKind(),
			resource.GetName(),
		),
	)()

	resourceInterface, err := client.GetDynamicResourceInterface(resource)
	if err != nil {
		return fmt.Errorf("failed to resolve resource: %w", err)
	}

	dryRun := func() []string {
		if opts.DryRun {
			return []string{metav1.DryRunAll}
		}
		return nil
	}()

	data, err := json.Marshal(resource)
	if err != nil {
		return err
	}

	_, err = resourceInterface.Patch(
		ctx,
		resource.GetName(),
		types.ApplyPatchType,
		data,
		metav1.PatchOptions{
			FieldManager: yoke,
			Force:        &opts.ForceConflicts,
			DryRun:       dryRun,
		},
	)
	return err
}

func (client Client) RemoveOrphans(ctx context.Context, previous, current []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	defer internal.DebugTimer(ctx, "remove orphaned resources")()

	set := make(map[string]struct{})
	for _, resource := range current {
		set[internal.Canonical(resource)] = struct{}{}
	}

	var errs []error
	var removedResources []*unstructured.Unstructured
	for _, resource := range previous {
		func() {
			name := internal.Canonical(resource)

			if _, ok := set[name]; ok {
				return
			}

			defer internal.DebugTimer(ctx, "delete resource "+name)()

			resourceInterface, err := client.GetDynamicResourceInterface(resource)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to resolve resource %s: %w", name, err))
				return
			}

			if err := resourceInterface.Delete(ctx, resource.GetName(), metav1.DeleteOptions{}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete %s: %w", name, err))
				return
			}

			removedResources = append(removedResources, resource)
		}()
	}

	return removedResources, xerr.MultiErrOrderedFrom("", errs...)
}

func (client Client) GetRevisions(ctx context.Context, release string) (*internal.Revisions, error) {
	defer internal.DebugTimer(ctx, "get revisions for "+release)

	mapping, err := client.mapper.RESTMapping(schema.GroupKind{Kind: "Secret"})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping for Secret: %w", err)
	}

	var labelSelector metav1.LabelSelector
	metav1.AddLabelToSelector(&labelSelector, internal.LabelRelease, release)

	list, err := client.meta.Resource(mapping.Resource).Namespace(NSKubeSystem).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&labelSelector),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list revision items: %w", err)
	}

	revisions := internal.Revisions{Release: release}
	for _, item := range list.Items {
		revisions.Add(internal.Revision{
			Name: item.Name,
			Source: internal.Source{
				Ref:      item.Annotations[internal.AnnotationSourceURL],
				Checksum: item.Annotations[internal.AnnotationSourceChecksum],
			},
			CreatedAt: internal.MustParseTime(item.Annotations[internal.AnnotationCreatedAt]),
			ActiveAt:  internal.MustParseTime(item.Annotations[internal.AnnotationActiveAt]),
			Resources: internal.MustParseInt(item.Annotations[internal.AnnotationResourceCount]),
		})
	}

	return &revisions, nil
}

func (client Client) DeleteRevisions(ctx context.Context, revisions internal.Revisions) error {
	defer internal.DebugTimer(ctx, "delete revision history "+revisions.Release)()

	secrets := client.clientset.CoreV1().Secrets(NSKubeSystem)

	var errs []error
	for _, revision := range revisions.History {
		if err := secrets.Delete(ctx, revision.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", revision.Name, err))
		}
	}

	return xerr.MultiErrOrderedFrom("removing revision history secrets", errs...)
}

func (client Client) GetAllRevisions(ctx context.Context) ([]internal.Revisions, error) {
	mapping, err := client.mapper.RESTMapping(schema.GroupKind{Kind: "Secret"})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping for Secret: %w", err)
	}

	var selector metav1.LabelSelector
	metav1.AddLabelToSelector(&selector, internal.LabelKind, "revision")

	list, err := client.meta.Resource(mapping.Resource).Namespace(NSKubeSystem).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&selector),
	})
	if err != nil {
		return nil, err
	}

	releases := map[string]struct{}{}
	for _, item := range list.Items {
		releases[item.Labels[internal.LabelRelease]] = struct{}{}
	}

	var result []internal.Revisions
	for release := range releases {
		revisions, err := client.GetRevisions(ctx, release)
		if err != nil {
			return nil, fmt.Errorf("failed to get revisions for release %s: %w", release, err)
		}
		result = append(result, *revisions)
	}

	return result, nil
}

func (client Client) CreateRevision(ctx context.Context, release string, revision internal.Revision, resources []*unstructured.Unstructured) error {
	name := yokePrefix(release) + "-" + internal.RandomString()

	data, err := json.Marshal(resources)
	if err != nil {
		return fmt.Errorf("failed to marshal resources: %w", err)
	}

	_, err = client.clientset.CoreV1().Secrets(NSKubeSystem).Create(
		ctx,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					internal.LabelKind:    "revision",
					internal.LabelRelease: release,
				},
				Annotations: map[string]string{
					internal.AnnotationCreatedAt:      revision.CreatedAt.Format(time.RFC3339),
					internal.AnnotationActiveAt:       revision.ActiveAt.Format(time.RFC3339),
					internal.AnnotationResourceCount:  strconv.Itoa(revision.Resources),
					internal.AnnotationSourceURL:      revision.Source.Ref,
					internal.AnnotationSourceChecksum: revision.Source.Checksum,
				},
			},
			StringData: map[string]string{
				internal.KeyResources: string(data),
			},
		},
		metav1.CreateOptions{FieldManager: yoke},
	)

	return err
}

func (client Client) UpdateRevisionActiveState(ctx context.Context, name string) error {
	secrets := client.clientset.CoreV1().Secrets(NSKubeSystem)

	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get revision secret: %w", err)
	}

	secret.Annotations[internal.AnnotationActiveAt] = time.Now().Format(time.RFC3339)

	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{FieldManager: yoke})
	return err
}

func (client Client) GetRevisionResources(ctx context.Context, revision internal.Revision) ([]*unstructured.Unstructured, error) {
	secret, err := client.clientset.CoreV1().Secrets(NSKubeSystem).Get(ctx, revision.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var resources []*unstructured.Unstructured
	err = json.Unmarshal(secret.Data[internal.KeyResources], &resources)

	return resources, err
}

func (client Client) GetDynamicResourceInterface(resource *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	apiResource, err := client.LookupResourceMapping(resource)
	if err != nil {
		return nil, err
	}
	if apiResource.Scope.Name() == meta.RESTScopeNameNamespace {
		return client.dynamic.Resource(apiResource.Resource).Namespace(resource.GetNamespace()), nil
	}
	return client.dynamic.Resource(apiResource.Resource), nil
}

func (client *Client) LookupResourceMapping(resource *unstructured.Unstructured) (*meta.RESTMapping, error) {
	gvk := schema.FromAPIVersionAndKind(resource.GetAPIVersion(), resource.GetKind())
	mapping, err := client.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil && meta.IsNoMatchError(err) {
		client.mapper.Reset()
		mapping, err = client.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	return mapping, err
}

func (client Client) UpdateResourceReleaseMapping(ctx context.Context, release string, create, remove []string) error {
	defer internal.DebugTimer(ctx, "update resource to release mapping")()

	configMaps := client.clientset.CoreV1().ConfigMaps(NSKubeSystem)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		configMap, err := configMaps.Get(ctx, ResourceReleaseMapping, metav1.GetOptions{})
		if kerrors.IsNotFound(err) {
			mapping := map[string]string{}
			for _, value := range create {
				mapping[value] = release
			}

			_, err := configMaps.Create(
				ctx,
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:   ResourceReleaseMapping,
						Labels: map[string]string{"internal.yoke/kind": "resource-mapping"},
					},
					Data: mapping,
				},
				metav1.CreateOptions{FieldManager: yoke},
			)
			return err
		}

		if err != nil {
			return fmt.Errorf("failed to get resource to release mapping: %w", err)
		}

		if configMap.Data == nil {
			configMap.Data = make(map[string]string, len(create))
		}

		for _, value := range remove {
			delete(configMap.Data, value)
		}
		for _, value := range create {
			configMap.Data[value] = release
		}

		_, err = configMaps.Update(ctx, configMap, metav1.UpdateOptions{FieldManager: yoke})
		return err
	})
}

func (client Client) GetResourceReleaseMapping(ctx context.Context) (map[string]string, error) {
	configMaps := client.clientset.CoreV1().ConfigMaps(NSKubeSystem)

	configMap, err := configMaps.Get(ctx, ResourceReleaseMapping, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	mapping := configMap.Data
	if mapping == nil {
		mapping = make(map[string]string)
	}

	return mapping, nil
}

func (client Client) ValidateOwnership(ctx context.Context, release string, resources []*unstructured.Unstructured) error {
	resourceReleaseMapping, err := client.GetResourceReleaseMapping(ctx)
	if err != nil {
		return fmt.Errorf("failed to get release to resource mapping: %w", err)
	}

	var errs []error
	for _, resource := range internal.CanonicalNameList(resources) {
		if currentRelease, ok := resourceReleaseMapping[resource]; ok && currentRelease != release {
			errs = append(errs, fmt.Errorf("resource %+q is owned by release %+q", resource, currentRelease))
		}
	}

	return xerr.MultiErrOrderedFrom("conflict(s)", errs...)
}

func (client Client) EnsureNamespace(ctx context.Context, namespace string) error {
	defer internal.DebugTimer(ctx, "ensuring namespace: "+namespace)()

	if _, err := client.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}

		if _, err := client.clientset.CoreV1().Namespaces().Create(
			ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}},
			metav1.CreateOptions{},
		); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
	}

	return nil
}

func (client Client) GetInClusterState(ctx context.Context, resource *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	defer internal.DebugTimer(ctx, "get in-cluster state for "+internal.Canonical(resource))()

	resourceInterface, err := client.GetDynamicResourceInterface(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic resource interface: %w", err)
	}

	state, err := resourceInterface.Get(ctx, resource.GetName(), metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		err = nil
	}

	return state, err
}

type WaitOptions struct {
	Timeout  time.Duration
	Interval time.Duration
}

func (client Client) WaitForReady(ctx context.Context, resource *unstructured.Unstructured, opts WaitOptions) error {
	defer internal.DebugTimer(ctx, fmt.Sprintf("waiting for %s to be ready", internal.Canonical(resource)))()

	// TODO: let user configure these values?
	var (
		interval = cmp.Or(opts.Interval, time.Second)
		timeout  = cmp.Or(opts.Timeout, 2*time.Minute)
	)

	timer := time.NewTimer(0)
	defer timer.Stop()

	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("%s timeout reached", timeout))
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
			state, err := client.GetInClusterState(ctx, resource)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("%w: %w", err, context.Cause(ctx))
				}
				return fmt.Errorf("failed to get in cluster state: %w", err)
			}

			if state == nil {
				return fmt.Errorf("resource not found")
			}

			if isReady(ctx, state) {
				return nil
			}

			timer.Reset(interval)
		}
	}
}

func (client Client) WaitForReadyMany(ctx context.Context, resources []*unstructured.Unstructured, opts WaitOptions) error {
	defer internal.DebugTimer(ctx, "waiting for resources to become ready")()

	var wg sync.WaitGroup
	wg.Add(len(resources))
	defer wg.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(resources))
	go func() {
		wg.Wait()
		close(errs)
	}()

	for _, resource := range resources {
		go func() {
			defer wg.Done()
			if err := client.WaitForReady(ctx, resource, opts); err != nil {
				errs <- fmt.Errorf("failed to get readiness for %s: %w", internal.Canonical(resource), err)
			}
		}()
	}

	return <-errs
}
