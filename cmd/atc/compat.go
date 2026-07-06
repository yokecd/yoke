package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type ReleaseNameUpdater k8s.Client

func (updater *ReleaseNameUpdater) Update(ctx context.Context) error {
	airways, err := (*k8s.Client)(updater).AirwayIntf().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list airways: %w", err)
	}

	for _, airway := range airways {
		if err := updater.updateAirway(ctx, airway); err != nil {
			return fmt.Errorf("failed to update airway %q: %w", airway.Name, err)
		}
	}

	return nil
}

func (updater *ReleaseNameUpdater) updateAirway(ctx context.Context, airway *v1alpha1.Airway) error {
	instanceIntf := func() dynamic.ResourceInterface {
		version, _ := internal.Find(airway.Spec.Template.Versions, func(version apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return version.Storage
		})
		intf := updater.Dynamic.Resource(schema.GroupVersionResource{
			Group:    airway.Spec.Template.Group,
			Version:  version.Name,
			Resource: airway.Spec.Template.Names.Plural,
		})
		if airway.Spec.Template.Scope == apiextensionsv1.ClusterScoped {
			return intf
		}
		return intf.Namespace("")
	}()

	instances, err := instanceIntf.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	for _, instance := range instances.Items {
		if err := updater.updateInstance(ctx, &instance); err != nil {
			return fmt.Errorf("failed to update instance: %q: %w", internal.ResourceRef(&instance), err)
		}
	}

	return nil
}

func (updater *ReleaseNameUpdater) updateInstance(ctx context.Context, instance *unstructured.Unstructured) error {
	secretIntf := updater.Clientset.CoreV1().Secrets(cmp.Or(instance.GetNamespace(), "default"))

	selector := metav1.FormatLabelSelector(
		&metav1.LabelSelector{
			MatchLabels: map[string]string{
				internal.LabelKind:    "revision",
				internal.LabelRelease: internal.SHA1HexFromString(deprecatedReleaseName(instance)),
			},
		},
	)

	revisions, err := secretIntf.List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("failed to list revision secrets: %w", err)
	}

	ref := internal.ResourceRef(instance)

	for _, revision := range revisions.Items {
		revision.Labels[internal.LabelRelease] = internal.SHA1HexFromString(ref)
		revision.Annotations[internal.AnnotationReleaseName] = ref
		if _, err := secretIntf.Update(ctx, &revision, metav1.UpdateOptions{FieldManager: "yoke"}); err != nil {
			return fmt.Errorf("failed to update revision metadata on %q: %w", revision.Name, err)
		}
	}

	return nil
}

func deprecatedReleaseName(resource *unstructured.Unstructured) string {
	gvk := resource.GroupVersionKind()
	elems := []string{
		gvk.Group,
		gvk.Kind,
	}

	if ns := resource.GetNamespace(); ns != "" {
		elems = append(elems, ns)
	}

	elems = append(elems, resource.GetName())

	return strings.Join(elems, ".")
}
