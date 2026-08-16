package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	lua "github.com/mmcdole/lunar"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/x/xsync"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	kcache "k8s.io/client-go/tools/cache"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
)

// isReady checks for readiness of workload resources, namespaces, and CRDs
func (client Client) isReady(ctx context.Context, resource *unstructured.Unstructured) (bool, error) {
	gvk := resource.GroupVersionKind()

	switch gvk.Group {
	case "":
		switch gvk.Kind {
		case "Namespace":
			phase, _, _ := unstructured.NestedString(resource.Object, "status", "phase")
			return phase == "Active", nil
		case "Pod":
			return MeetsConditions(resource, "Initialized", "ContainersReady", "Ready"), nil
		case "Service":
			endpoints, err := client.Clientset.DiscoveryV1().EndpointSlices(resource.GetNamespace()).List(ctx, metav1.ListOptions{
				LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{discoveryv1.LabelServiceName: resource.GetName()},
				}),
			})
			return endpoints != nil && len(endpoints.Items) > 0, err
		}
	case "apps":
		switch gvk.Kind {
		case "Deployment":
			return true &&
				observedGeneration(resource) == resource.GetGeneration() &&
				MeetsConditions(resource, "Available") &&
				equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas"), nil
		case "ReplicaSet", "StatefulSet":
			return observedGeneration(resource) == resource.GetGeneration() &&
				equalInts(resource, "replicas", "availableReplicas", "readyReplicas", "updatedReplicas"), nil
		case "DaemonSet":
			return observedGeneration(resource) == resource.GetGeneration() &&
				equalInts(resource, "currentNumberScheduled", "desiredNumberScheduled", "updatedNumberScheduled", "numberAvailable", "numberReady"), nil
		}
	case "batch":
		switch gvk.Kind {
		case "Job":
			if MeetsConditions(resource, "Failed") {
				return false, errors.New("job has failed")
			}
			return MeetsConditions(resource, "Complete"), nil
		}
	case "apiextensions.k8s.io":
		switch gvk.Kind {
		case "CustomResourceDefinition":
			return MeetsConditions(resource, "Established"), nil
		}
	case "yoke.cd":
		switch gvk.Kind {
		case "Airway", "Flight", "ClusterFlight":
			return MeetsConditions(resource, "Ready"), nil
		}
	}

	// if the resource is owned by an airway, it is an instance of that airway and so uses standard flight status.
	if _, ok := internal.Find(resource.GetOwnerReferences(), func(ref metav1.OwnerReference) bool {
		return ref.APIVersion == v1alpha1.APIVersion && ref.Kind == v1alpha1.KindAirway
	}); ok {
		return MeetsConditions(resource, "Ready"), nil
	}

	if customReadiness := getCustomReadiness(ctx); customReadiness != nil {
		gk := resource.GroupVersionKind().GroupKind()
		if customCheck, ok := customReadiness.Load(gk); ok {
			return customCheck(resource)
		}
		if customCheck, ok := customReadiness.Load(schema.GroupKind{Kind: "_", Group: gk.Group}); ok {
			return customCheck(resource)
		}
	}

	return true, nil
}

func MeetsConditions(resource *unstructured.Unstructured, conditions ...string) bool {
	statusObj, ok, _ := unstructured.NestedMap(resource.Object, "status")
	if !ok {
		return false
	}

	var status struct {
		Conditions []metav1.Condition `json:"conditions"`
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(statusObj, &status); err != nil {
		return false
	}

	for _, conditionType := range conditions {
		condition := meta.FindStatusCondition(status.Conditions, conditionType)
		if condition == nil {
			return false
		}
		if condition.ObservedGeneration > 0 && condition.ObservedGeneration != resource.GetGeneration() {
			return false
		}
		if condition.Status != metav1.ConditionTrue {
			return false
		}
	}

	return true
}

func equalInts(resource *unstructured.Unstructured, keys ...string) bool {
	if len(keys) == 0 {
		return true
	}

	values := []int64{}
	for _, key := range keys {
		value, _, _ := unstructured.NestedInt64(resource.Object, "status", key)
		values = append(values, value)
	}

	wanted := values[0]
	for _, value := range values[1:] {
		if value != wanted {
			return false
		}
	}

	return true
}

func observedGeneration(resource *unstructured.Unstructured) int64 {
	value, _, _ := unstructured.NestedInt64(resource.Object, "status", "observedGeneration")
	return value
}

type CustomReadiness = xsync.Map[schema.GroupKind, func(*unstructured.Unstructured) (bool, error)]

type customReadinesskey struct{}

func WithCustomReadiness(ctx context.Context, source *CustomReadiness) context.Context {
	return context.WithValue(ctx, customReadinesskey{}, source)
}

func getCustomReadiness(ctx context.Context) *CustomReadiness {
	if source, ok := ctx.Value(customReadinesskey{}).(*CustomReadiness); ok && source != nil {
		return source
	}
	return nil
}

const LabelResourceReadiness = "resource.yoke.cd/readiness"

var selectorResourceReadiness = metav1.LabelSelector{
	MatchExpressions: []metav1.LabelSelectorRequirement{
		{
			Key:      LabelResourceReadiness,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{"lua", "conditions"},
		},
	},
}

func (client *Client) LoadCustomReadinessFuncs(ctx context.Context) (*CustomReadiness, error) {
	configmaps, err := client.Clientset.CoreV1().ConfigMaps(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&selectorResourceReadiness),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list readiness configmaps: %w", err)
	}

	var readiness CustomReadiness
	for _, cm := range configmaps.Items {
		registerReadinessConfigMap(&readiness, &cm)
	}

	return &readiness, nil
}

func (client *Client) WatchCustomReadiness(ctx context.Context) (result *CustomReadiness, stop func(), err error) {
	var readiness CustomReadiness

	factory := informers.NewSharedInformerFactoryWithOptions(
		client.Clientset,
		0,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(&selectorResourceReadiness)
		}),
	)

	factory.Core().V1().ConfigMaps().Informer().AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			registerReadinessConfigMap(&readiness, obj.(*corev1.ConfigMap))
		},
		UpdateFunc: func(_, obj any) {
			registerReadinessConfigMap(&readiness, obj.(*corev1.ConfigMap))
		},
		DeleteFunc: func(obj any) {
			for gk := range obj.(*corev1.ConfigMap).Data {
				readiness.Delete(schema.ParseGroupKind(gk))
			}
		},
	})

	ctx, cancel := context.WithCancel(ctx)

	stop = func() {
		cancel()
		factory.Shutdown()
	}

	defer func() {
		if err != nil {
			stop()
		}
	}()

	factory.StartWithContext(ctx)

	if err := factory.WaitForCacheSyncWithContext(ctx).Err; err != nil {
		return nil, nil, err
	}

	return &readiness, stop, nil
}

func registerReadinessConfigMap(readiness *CustomReadiness, configMap *corev1.ConfigMap) {
	label := configMap.Labels["resource.yoke.cd/readiness"]
	for gk, value := range configMap.Data {
		readiness.Store(schema.ParseGroupKind(gk), func() func(*unstructured.Unstructured) (bool, error) {
			if label == "conditions" {
				return func(resource *unstructured.Unstructured) (bool, error) {
					return MeetsConditions(resource, strings.Fields(value)...), nil
				}
			}
			return func(u *unstructured.Unstructured) (ready bool, err error) {
				state, err := lua.New(lua.Options{
					Libraries: lua.CoreLibraries(),
					Stdout:    io.Discard,
					Stderr:    io.Discard,
					Stdin:     strings.NewReader(""),
				})
				if err != nil {
					return false, fmt.Errorf("failed to initialize lua state: %w", err)
				}
				defer func() { err = xerr.Join(err, state.Close()) }()

				chunk, err := state.LoadString(gk, value)
				if err != nil {
					return false, fmt.Errorf("failed to load lua script: %w", err)
				}

				fn, err := state.CallOne(chunk.Value())
				if err != nil {
					return false, fmt.Errorf("failed to execute lua script: %w", err)
				}

				resource, err := state.NewTableFrom(u.Object)
				if err != nil {
					return false, fmt.Errorf("failed to convert resource into lua table: %w", err)
				}

				value, err := state.CallOne(fn, resource.Value())
				if err != nil {
					return false, fmt.Errorf("failed to invoke lua readiness function: %w", err)
				}

				if value.IsNil() {
					return false, nil
				}

				ready, ok := value.AsBool()
				if !ok {
					return false, fmt.Errorf("expected result to be a boolean but got: %s", value.Kind())
				}

				return ready, nil
			}
		}())
	}
}
