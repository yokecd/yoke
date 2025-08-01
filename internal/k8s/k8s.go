package k8s

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
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
	"k8s.io/cli-runtime/pkg/genericclioptions"
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

func NewClientFromConfigFlags(cfgFlags *genericclioptions.ConfigFlags) (*Client, error) {
	restcfg, err := cfgFlags.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build k8 config: %w", err)
	}
	return NewClient(restcfg)
}

func NewClientFromKubeConfig(path string) (*Client, error) {
	restcfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("failed to build k8 config: %w", err)
	}
	return NewClient(restcfg)
}

func NewClient(cfg *rest.Config) (*Client, error) {
	cfg.Burst = cmp.Or(cfg.Burst, 300)
	cfg.QPS = cmp.Or(cfg.QPS, 50)

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
	SkipDryRun bool
	ApplyOpts
}

func (client Client) ApplyResources(ctx context.Context, resources []*unstructured.Unstructured, opts ApplyResourcesOpts) error {
	defer internal.DebugTimer(ctx, "apply resources")()

	applyOpts := opts.ApplyOpts

	if opts.DryRun || !opts.SkipDryRun {
		applyOpts := applyOpts
		applyOpts.DryRun = true

		if err := xerr.MultiErrOrderedFrom("dry run", client.applyMany(ctx, resources, applyOpts)...); err != nil {
			return err
		}
		if opts.DryRun {
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
	ForceOwnership bool
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

	if err := client.checkOwnership(ctx, resource, opts); err != nil {
		return fmt.Errorf("failed to validate resource release: %w", err)
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

func (client Client) checkOwnership(ctx context.Context, resource *unstructured.Unstructured, opts ApplyOpts) error {
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

	localOwner := internal.GetOwner(resource)
	svrOwner := internal.GetOwner(svrResource)

	if !opts.ForceOwnership && localOwner != svrOwner {
		return fmt.Errorf("expected release %q but resource is already owned by %q", localOwner, svrOwner)
	}

	return nil
}

type PruneOpts struct {
	RemoveCRDs       bool
	RemoveNamespaces bool
}

func (client Client) PruneReleaseDiff(ctx context.Context, previous, next internal.Stages, opts PruneOpts) (removed, orphaned []*unstructured.Unstructured, err error) {
	defer internal.DebugTimer(ctx, "prune release diff")()

	curentSet := make(map[string]*unstructured.Unstructured)
	for _, resource := range next.Flatten() {
		curentSet[internal.CanonicalWithoutVersion(resource)] = resource
	}

	var errs []error

	for _, stage := range slices.Backward(previous) {
		for _, resource := range stage {
			func() {
				name := internal.CanonicalWithoutVersion(resource)
				if _, ok := curentSet[name]; ok {
					return
				}

				if (!opts.RemoveCRDs && internal.IsCRD(resource)) || (!opts.RemoveNamespaces && internal.IsNamespace(resource)) {
					if err := client.OrhpanResource(ctx, resource); err != nil {
						errs = append(errs, fmt.Errorf("failed to orphan resource: %s: %w", name, err))
					}
					orphaned = append(orphaned, resource)
					return
				}

				defer internal.DebugTimer(ctx, "delete resource "+name)()

				intf, err := client.GetDynamicResourceInterface(resource)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to resolve resource %s: %w", name, err))
					return
				}

				clusterState, err := intf.Get(ctx, resource.GetName(), metav1.GetOptions{})
				if err != nil && !kerrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to lookup %s: %w", name, err))
				}

				if internal.GetOwner(clusterState) != internal.GetOwner(resource) {
					return
				}

				if err := intf.Delete(ctx, resource.GetName(), metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to delete %s: %w", name, err))
					return
				}

				removed = append(removed, resource)
			}()
		}
	}

	return removed, orphaned, xerr.MultiErrOrderedFrom("", errs...)
}

func (client Client) GetRelease(ctx context.Context, name, ns string) (*internal.Release, error) {
	defer internal.DebugTimer(ctx, "get revisions for "+name)

	mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Kind: "Secret"})
	if err != nil {
		return nil, fmt.Errorf("failed to get resource mapping for Secret: %w", err)
	}

	var labelSelector metav1.LabelSelector
	metav1.AddLabelToSelector(&labelSelector, internal.LabelRelease, name)

	list, err := client.Meta.Resource(mapping.Resource).Namespace(ns).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&labelSelector),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list revision items: %w", err)
	}

	release := internal.Release{Name: name, Namespace: ns}

	for _, item := range list.Items {
		release.Add(internal.Revision{
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

	// The kubernetes API lists object in roughly creation order as per etcd but does not guarantee it.
	slices.SortStableFunc(release.History, func(a, b internal.Revision) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	return &release, nil
}

func (client Client) DeleteRevisions(ctx context.Context, revisions internal.Release) error {
	defer internal.DebugTimer(ctx, "delete revision history "+revisions.Name)()

	secrets := client.Clientset.CoreV1().Secrets(revisions.Namespace)

	var errs []error
	for _, revision := range revisions.History {
		if err := secrets.Delete(ctx, revision.Name, metav1.DeleteOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", revision.Name, err))
		}
	}

	return xerr.MultiErrOrderedFrom("removing revision history secrets", errs...)
}

func (client Client) GetReleases(ctx context.Context) ([]internal.Release, error) {
	list, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(list.Items))
	revisions := make([][]internal.Release, len(list.Items))

	for i, ns := range list.Items {
		wg.Add(1)

		go func() {
			defer wg.Done()

			result, err := client.GetReleasesByNS(ctx, ns.Name)
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

	var result []internal.Release
	for _, revision := range revisions {
		result = append(result, revision...)
	}

	return result, nil
}

func (client Client) GetReleasesByNS(ctx context.Context, ns string) ([]internal.Release, error) {
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

	var result []internal.Release
	for release := range releases {
		revisions, err := client.GetRelease(ctx, release, ns)
		if err != nil {
			return nil, fmt.Errorf("failed to get revisions for release %s: %w", release, err)
		}
		result = append(result, *revisions)
	}

	return result, nil
}

func (client Client) CreateRevision(ctx context.Context, release, ns string, revision internal.Revision, stages internal.Stages) error {
	data, err := json.Marshal(stages)
	if err != nil {
		return fmt.Errorf("failed to marshal resources: %w", err)
	}

	data = func() []byte {
		var buffer bytes.Buffer
		w := gzip.NewWriter(&buffer)
		// Since we are only reading to in memory data structures it is safe to ignore potential
		// write errors. The only way they can fail is if the system is out of memory. If that is the case,
		// we have bigger fish to fry.
		_, _ = w.Write(data)
		_ = w.Close()
		return buffer.Bytes()
	}()

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
					internal.AnnotationCreatedAt:      revision.CreatedAt.Format(time.RFC3339Nano),
					internal.AnnotationActiveAt:       revision.ActiveAt.Format(time.RFC3339Nano),
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

func (client Client) CapReleaseHistory(ctx context.Context, name, ns string, size int) error {
	release, err := client.GetRelease(ctx, name, ns)
	if err != nil {
		return fmt.Errorf("failed to load release: %w", err)
	}
	slices.SortStableFunc(release.History, func(a, b internal.Revision) int {
		return b.ActiveAt.Compare(a.ActiveAt)
	})

	// Nothing to delete
	if size >= len(release.History) {
		return nil
	}

	secretIntf := client.Clientset.CoreV1().Secrets(ns)

	var errs []error
	for _, revision := range release.History[size:] {
		if err := secretIntf.Delete(ctx, revision.Name, metav1.DeleteOptions{}); err != nil {
			if kerrors.IsNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", revision.Name, err))
		}
	}

	return xerr.MultiErrFrom("deleting release history", errs...)
}

var ErrLockTaken = errors.New("lock is already taken")

func (client Client) LockRelease(ctx context.Context, release internal.Release) error {
	secretIntf := client.Clientset.CoreV1().Secrets(release.Namespace)

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "yoke." + internal.SHA1HexString([]byte(release.Name)),
			Labels: map[string]string{
				internal.LabelKind:    "lock",
				internal.LabelRelease: release.Name,
			},
		},
	}

	if _, err := secretIntf.Create(ctx, secret, metav1.CreateOptions{FieldManager: yoke}); err != nil {
		if kerrors.IsAlreadyExists(err) {
			return ErrLockTaken
		}
		return fmt.Errorf("failed to create secret: %w", err)
	}

	return nil
}

func (client Client) UnlockRelease(ctx context.Context, release internal.Release) error {
	secretIntf := client.Clientset.CoreV1().Secrets(release.Namespace)
	if err := secretIntf.Delete(ctx, "yoke."+internal.SHA1HexString([]byte(release.Name)), metav1.DeleteOptions{}); err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func (client Client) GetRevisionResources(ctx context.Context, revision internal.Revision) (internal.Stages, error) {
	secret, err := client.Clientset.CoreV1().Secrets(revision.Namespace).Get(ctx, revision.Name, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	data, err := func() ([]byte, error) {
		raw := secret.Data[internal.KeyResources]
		r, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			// If it fails to decode the bytes as valid gzip, the secret is from a previous version of yoke
			// that did not gzip resource content. Use as is; this way we maintain backward compatibility.
			return raw, nil
		}
		return io.ReadAll(r)
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to read secret data: %w", err)
	}

	var stages internal.Stages
	err = json.Unmarshal(data, &stages)

	return stages, err
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

	var cancel context.CancelFunc
	if timeout >= 0 {
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("%s timeout reached", timeout))
		defer cancel()
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

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

	return client.isReady(ctx, state)
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

// NoTimeout as a timeout values will cause Wait for ready to never timeout. Any negative number will do.
// This constant is added for semantic clarity.
const NoTimeout = -1

type PatchConfig struct {
	Remove []string
}

func (patch PatchConfig) MarshalJSON() ([]byte, error) {
	type patchOp struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value,omitempty"`
	}

	patches := []patchOp{}
	for _, path := range patch.Remove {
		patches = append(patches, patchOp{
			Op:   "remove",
			Path: path,
		})
	}

	return json.Marshal(patches)
}

func (client Client) Patch(ctx context.Context, resource *unstructured.Unstructured, patch PatchConfig) error {
	intf, err := client.GetDynamicResourceInterface(resource)
	if err != nil {
		return fmt.Errorf("failed to get dynamic resource interface: %w", err)
	}

	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("unexpected error serializing patch config: %w", err)
	}

	_, err = intf.Patch(ctx, resource.GetName(), types.JSONPatchType, data, metav1.PatchOptions{FieldManager: yoke})
	return err
}

func (client Client) PatchMany(ctx context.Context, resources []*unstructured.Unstructured, patch PatchConfig) error {
	var wg sync.WaitGroup
	wg.Add(len(resources))

	semaphore := make(chan struct{}, runtime.GOMAXPROCS(-1))
	errs := make([]error, len(resources))

	for i, resource := range resources {
		go func() {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if err := client.Patch(ctx, resource, patch); err != nil {
				errs[i] = fmt.Errorf("%s: %w", internal.Canonical(resource), err)
			}
		}()
	}

	wg.Wait()

	return xerr.MultiErrFrom("", errs...)
}

func (client Client) OrhpanResource(ctx context.Context, resource *unstructured.Unstructured) error {
	resource, err := client.GetInClusterState(ctx, resource)
	if err != nil {
		return fmt.Errorf("failed to get in cluster state: %w", err)
	}
	if labels := resource.GetLabels(); labels != nil {
		if _, ok := labels[internal.LabelManagedBy]; !ok {
			return nil
		}
	}

	paths := []string{
		fmt.Sprintf("/metadata/labels/%s", strings.ReplaceAll(internal.LabelManagedBy, "/", "~1")),
		fmt.Sprintf("/metadata/labels/%s", strings.ReplaceAll(internal.LabelYokeRelease, "/", "~1")),
		fmt.Sprintf("/metadata/labels/%s", strings.ReplaceAll(internal.LabelYokeReleaseNS, "/", "~1")),
	}

	if len(resource.GetOwnerReferences()) > 0 {
		paths = append(paths, "/metadata/ownerReferences")
	}

	return client.Patch(ctx, resource, PatchConfig{Remove: paths})
}
