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

	"github.com/davidmdm/x/xerr"

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

	"github.com/yokecd/yoke/internal"
)

const (
	yoke = "yoke"
)

type Client struct {
	Dynamic   *dynamic.DynamicClient
	Clientset *kubernetes.Clientset
	Meta      metadata.Interface
	Mapper    *restmapper.DeferredDiscoveryRESTMapper
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
		Dynamic:   dynamicClient,
		Clientset: clientset,
		Meta:      meta,
		Mapper:    restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(clientset.DiscoveryClient)),
	}, nil
}

type ApplyResourcesOpts struct {
	DryRunOnly     bool
	SkipDryRun     bool
	ForceConflicts bool
	Release        string
}

func (client Client) ApplyResources(ctx context.Context, resources []*unstructured.Unstructured, opts ApplyResourcesOpts) error {
	defer internal.DebugTimer(ctx, "apply resources")()

	applyOpts := ApplyOpts{
		ForceConflicts: opts.ForceConflicts,
		Release:        opts.Release,
	}

	if opts.DryRunOnly || !opts.SkipDryRun {
		applyOpts := applyOpts
		applyOpts.DryRun = true

		if err := xerr.MultiErrOrderedFrom("dry run", client.applyMany(ctx, resources, applyOpts)...); err != nil {
			return err
		}
		if opts.DryRunOnly {
			return nil
		}
	}

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
	Release        string
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

	intf, err := client.GetDynamicResourceInterface(resource)
	if err != nil {
		return fmt.Errorf("failed to resolve resource: %w", err)
	}

	if release := opts.Release; release != "" {
		if err := client.checkResourceRelease(ctx, release, resource); err != nil {
			return fmt.Errorf("failed to validate resource release: %w", err)
		}
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

	_, err = intf.Patch(
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

func (client Client) checkResourceRelease(ctx context.Context, targetRelease string, resource *unstructured.Unstructured) error {
	intf, err := client.GetDynamicResourceInterface(resource)
	if err != nil {
		return fmt.Errorf("failed to get dynamic resource interface: %w", err)
	}

	svrResource, err := intf.Get(ctx, resource.GetName(), metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	if serverRelease := svrResource.GetLabels()[internal.LabelYokeRelease]; serverRelease != targetRelease {
		return fmt.Errorf("expected release %q but resource is already owned by %q", targetRelease, serverRelease)
	}

	return nil
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

func (client Client) GetRevisions(ctx context.Context, release, ns string) (*internal.Revisions, error) {
	defer internal.DebugTimer(ctx, "get revisions for "+release)

	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Kind: "Secret"})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping for Secret: %w", err)
	}

	var labelSelector metav1.LabelSelector
	metav1.AddLabelToSelector(&labelSelector, internal.LabelRelease, release)

	list, err := client.Meta.Resource(mapping.Resource).Namespace(ns).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&labelSelector),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list revision items: %w", err)
	}

	revisions := internal.Revisions{Release: release, Namespace: ns}
	for _, item := range list.Items {
		revisions.Add(internal.Revision{
			Name:      item.Name,
			Namespace: ns,
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

	secrets := client.Clientset.CoreV1().Secrets(revisions.Namespace)

	var errs []error
	for _, revision := range revisions.History {
		if err := secrets.Delete(ctx, revision.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", revision.Name, err))
		}
	}

	return xerr.MultiErrOrderedFrom("removing revision history secrets", errs...)
}

func (client Client) GetAllRevisions(ctx context.Context) ([]internal.Revisions, error) {
	list, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(list.Items))
	revisions := make([][]internal.Revisions, len(list.Items))

	for i, ns := range list.Items {
		wg.Add(1)

		go func() {
			defer wg.Done()

			result, err := client.GetAllRevisionInNS(ctx, ns.Name)
			if err != nil {
				if kerrors.IsNotFound(err) || kerrors.IsForbidden(err) {
					// If is forbidden the user simply does not have access to these revisions and can simply ignore.
					return
				}
				errs[i] = fmt.Errorf("%s: %w", ns.Name, err)
				return
			}
			revisions[i] = result
		}()
	}

	wg.Wait()

	if err := xerr.MultiErrOrderedFrom("loading revisions from namespaces", errs...); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, err
	}

	var result []internal.Revisions
	for _, revision := range revisions {
		result = append(result, revision...)
	}

	return result, nil
}

func (client Client) GetAllRevisionInNS(ctx context.Context, ns string) ([]internal.Revisions, error) {
	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Kind: "Secret"})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping for Secret: %w", err)
	}

	var selector metav1.LabelSelector
	metav1.AddLabelToSelector(&selector, internal.LabelKind, "revision")

	list, err := client.Meta.Resource(mapping.Resource).Namespace(ns).List(ctx, metav1.ListOptions{
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
		revisions, err := client.GetRevisions(ctx, release, ns)
		if err != nil {
			return nil, fmt.Errorf("failed to get revisions for release %s: %w", release, err)
		}
		result = append(result, *revisions)
	}

	return result, nil
}

func (client Client) CreateRevision(ctx context.Context, release, ns string, revision internal.Revision, resources []*unstructured.Unstructured) error {
	data, err := json.Marshal(resources)
	if err != nil {
		return fmt.Errorf("failed to marshal resources: %w", err)
	}

	_, err = client.Clientset.CoreV1().Secrets(ns).Create(
		ctx,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "yoke." + internal.RandomString(),
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
			Data: map[string][]byte{
				internal.KeyResources: data,
			},
		},
		metav1.CreateOptions{FieldManager: yoke},
	)

	return err
}

func (client Client) UpdateRevisionActiveState(ctx context.Context, revision internal.Revision) error {
	secrets := client.Clientset.CoreV1().Secrets(revision.Namespace)

	secret, err := secrets.Get(ctx, revision.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get revision secret: %w", err)
	}

	secret.Annotations[internal.AnnotationActiveAt] = time.Now().Format(time.RFC3339)

	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{FieldManager: yoke})
	return err
}

func (client Client) GetRevisionResources(ctx context.Context, revision internal.Revision) ([]*unstructured.Unstructured, error) {
	secret, err := client.Clientset.CoreV1().Secrets(revision.Namespace).Get(ctx, revision.Name, metav1.GetOptions{})
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
		return client.Dynamic.Resource(apiResource.Resource).Namespace(resource.GetNamespace()), nil
	}
	return client.Dynamic.Resource(apiResource.Resource), nil
}

func (client *Client) LookupResourceMapping(resource *unstructured.Unstructured) (*meta.RESTMapping, error) {
	gvk := schema.FromAPIVersionAndKind(resource.GetAPIVersion(), resource.GetKind())
	mapping, err := client.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil && meta.IsNoMatchError(err) {
		client.Mapper.Reset()
		mapping, err = client.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	return mapping, err
}

func (client Client) EnsureNamespace(ctx context.Context, namespace string) error {
	defer internal.DebugTimer(ctx, "ensuring namespace: "+namespace)()

	if _, err := client.Clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}

		if _, err := client.Clientset.CoreV1().Namespaces().Create(
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
			ready, err := client.IsReady(ctx, resource)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
			timer.Reset(interval)
		}
	}
}

func (client Client) IsReady(ctx context.Context, resource *unstructured.Unstructured) (bool, error) {
	state, err := client.GetInClusterState(ctx, resource)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%w: %w", err, context.Cause(ctx))
		}
		return false, fmt.Errorf("failed to get in cluster state: %w", err)
	}

	if state == nil {
		return false, fmt.Errorf("resource not found")
	}

	return isReady(ctx, state), nil
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
