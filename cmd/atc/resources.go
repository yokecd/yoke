package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"
)

func ApplyResources(ctx context.Context, client *k8s.Client, cfg *Config) error {
	var (
		group = "yoke.cd"
		names = apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "airways",
			Singular: "airway",
			Kind:     "Airway",
		}
		forceful = k8s.ApplyOpts{
			ForceConflicts: true,
			ForceOwnership: true,
		}
	)

	airwayDef := &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: apiextensionsv1.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: names.Plural + "." + group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: names,
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[v1alpha1.Airway]()),
					},
					AdditionalPrinterColumns: []apiextensionsv1.CustomResourceColumnDefinition{
						{
							Name:     "flight",
							Type:     "string",
							JSONPath: ".spec.wasmUrls.flight",
						},
						{
							Name:     "mode",
							Type:     "string",
							JSONPath: ".spec.mode",
						},
						{
							Name:     "cluster_access",
							Type:     "boolean",
							JSONPath: ".spec.clusterAccess",
						},
						{
							Name:     "fix_drift_interval",
							Type:     "string",
							Format:   "date",
							JSONPath: ".spec.fixDriftInterval",
						},
					},
				},
			},
		},
	}

	airwayResource, err := internal.ToUnstructured(airwayDef)
	if err != nil {
		return fmt.Errorf("failed to convert airway crd to its unstructured representation: %w", err)
	}

	if err := client.ApplyResource(ctx, airwayResource, forceful); err != nil {
		return fmt.Errorf("failed to apply airway crd: %w", err)
	}

	airwayValidation := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionregistrationv1.SchemeGroupVersion.Identifier(),
			Kind:       "ValidatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "atc-airway",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "airways.yoke.cd",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: cfg.Service.Namespace,
						Name:      cfg.Service.Name,
						Path:      ptr.To("/validations/airways.yoke.cd"),
						Port:      &cfg.Service.Port,
					},
					CABundle: cfg.Service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"yoke.cd"},
							APIVersions: []string{"v1alpha1"},
							Resources:   []string{"airways"},
							Scope:       ptr.To(admissionregistrationv1.ClusterScope),
						},
					},
				},
			},
		},
	}

	resourceValidation := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionregistrationv1.SchemeGroupVersion.Identifier(),
			Kind:       "ValidatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "atc-resources",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "resources.yoke.cd",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: cfg.Service.Namespace,
						Name:      cfg.Service.Name,
						Path:      ptr.To("/validations/resources"),
						Port:      &cfg.Service.Port,
					},
					CABundle: cfg.Service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				FailurePolicy:           ptr.To(admissionregistrationv1.Ignore),
				MatchPolicy:             ptr.To(admissionregistrationv1.Exact),
				MatchConditions: []admissionregistrationv1.MatchCondition{
					{
						Name:       "managed-by-atc",
						Expression: `object.metadata.labels["app.kubernetes.io/managed-by"] == "atc.yoke" || oldObject.metadata.labels["app.kubernetes.io/managed-by"] == "atc.yoke"`,
					},
					{
						Name: "not-atc-service-account",
						Expression: fmt.Sprintf(
							`request.userInfo.username != "system:serviceaccount:%s:%s-service-account"`,
							cfg.Service.Namespace,
							cfg.Service.Name,
						),
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Update,
							admissionregistrationv1.Delete,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"*"},
							APIVersions: []string{"*"},
							Resources:   []string{"*/*"},
							Scope:       ptr.To(admissionregistrationv1.AllScopes),
						},
					},
				},
			},
		},
	}

	externalResourceValidation := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionregistrationv1.SchemeGroupVersion.Identifier(),
			Kind:       "ValidatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "atc-external-resources",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "external.resources.yoke.cd",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: cfg.Service.Namespace,
						Name:      cfg.Service.Name,
						Path:      ptr.To("/validations/external-resources"),
						Port:      &cfg.Service.Port,
					},
					CABundle: cfg.Service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				FailurePolicy:           ptr.To(admissionregistrationv1.Ignore),
				MatchPolicy:             ptr.To(admissionregistrationv1.Exact),
				TimeoutSeconds:          ptr.To[int32](1),
				MatchConditions: []admissionregistrationv1.MatchCondition{
					{
						Name: "all",
						Expression: And(
							Or(
								`object.kind != "Lease" && !object.apiVersion.startsWith("coordination.k8s.io/")`,
								`oldObject.kind != "Lease" && !oldObject.apiVersion.startsWith("coordination.k8s.io/")`,
							),
							Or(
								`object.kind != "Node" && object.apiVersion != ""`,
								`oldObject.kind != "Node" && oldObject.apiVersion != ""`,
							),
						),
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Update,
							admissionregistrationv1.Delete,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"*"},
							APIVersions: []string{"*"},
							Resources:   []string{"*/*"},
							Scope:       ptr.To(admissionregistrationv1.AllScopes),
						},
					},
				},
			},
		},
	}

	var webhooks []*unstructured.Unstructured
	for _, webhook := range []*admissionregistrationv1.ValidatingWebhookConfiguration{airwayValidation, resourceValidation, externalResourceValidation} {
		resource, err := internal.ToUnstructured(webhook)
		if err != nil {
			return fmt.Errorf("failed to convert webhook configuration to unstructured representation: %s: %w", webhook.Name, err)
		}
		webhooks = append(webhooks, resource)
	}

	if err := client.ApplyResources(ctx, webhooks, k8s.ApplyResourcesOpts{ApplyOpts: forceful}); err != nil {
		return fmt.Errorf("failed to apply webhooks: %w", err)
	}

	return nil
}

func And(expressions ...string) string {
	return fmt.Sprintf("(%s)", strings.Join(expressions, " && "))
}

func Or(expressions ...string) string {
	return fmt.Sprintf("(%s)", strings.Join(expressions, " || "))
}
