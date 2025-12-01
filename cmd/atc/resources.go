package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
	"github.com/yokecd/yoke/pkg/openapi"
)

func ApplyResources(ctx context.Context, client *k8s.Client, cfg *Config) (teardown func(ctx context.Context) error, err error) {
	// I don't generally approve of the panic/recover setup for returning errors.
	// But I am trying it because converting between typed and unstructured apis is too painful
	// in this function.
	defer func() {
		if err != nil {
			return
		}
		if recovered, ok := recover().(error); ok {
			err = recovered
		} else if recovered != nil {
			panic(recovered)
		}
	}()

	var (
		group       = "yoke.cd"
		airwayNames = apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "airways",
			Singular: "airway",
			Kind:     "Airway",
		}
		forceful = k8s.ApplyOpts{
			ForceConflicts: true,
			ForceOwnership: true,
		}
	)

	airwayResource := internal.Must2(internal.ToUnstructured(&apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: apiextensionsv1.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: airwayNames.Plural + "." + group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: airwayNames,
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
	}))

	flightResources := func() (resources []*unstructured.Unstructured) {
		type Def struct {
			apiextensionsv1.CustomResourceDefinitionNames
			Scope apiextensionsv1.ResourceScope
		}
		for _, def := range []Def{
			{
				CustomResourceDefinitionNames: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "flights",
					Singular: "flight",
					Kind:     "Flight",
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
			{
				CustomResourceDefinitionNames: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "clusterflights",
					Singular: "clusterflight",
					Kind:     "ClusterFlight",
				},
				Scope: apiextensionsv1.ClusterScoped,
			},
		} {
			resources = append(resources, internal.Must2(internal.ToUnstructured(
				&apiextensionsv1.CustomResourceDefinition{
					TypeMeta: metav1.TypeMeta{
						Kind:       "CustomResourceDefinition",
						APIVersion: apiextensionsv1.SchemeGroupVersion.Identifier(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: def.Plural + ".yoke.cd",
					},
					Spec: apiextensionsv1.CustomResourceDefinitionSpec{
						Group: group,
						Names: def.CustomResourceDefinitionNames,
						Scope: def.Scope,
						Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
							{
								Name:         "v1alpha1",
								Served:       true,
								Storage:      true,
								Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
								Schema:       &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: openapi.SchemaFrom(reflect.TypeFor[v1alpha1.Flight]())},
								AdditionalPrinterColumns: []apiextensionsv1.CustomResourceColumnDefinition{
									{
										Name:     "flight",
										Type:     "string",
										JSONPath: ".spec.wasmUrl",
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
				},
			)))
		}
		return
	}()

	crds := append(flightResources, airwayResource)

	if err := client.ApplyResources(ctx, crds, k8s.ApplyResourcesOpts{ApplyOpts: forceful}); err != nil {
		return nil, fmt.Errorf("failed to apply airway crd: %w", err)
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

	flightValidation := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: admissionregistrationv1.SchemeGroupVersion.Identifier(),
			Kind:       "ValidatingWebhookConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "atc-flight",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "flights.yoke.cd",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: cfg.Service.Namespace,
						Name:      cfg.Service.Name,
						Path:      ptr.To("/validations/flights.yoke.cd"),
						Port:      &cfg.Service.Port,
					},
					CABundle: cfg.Service.CABundle,
				},
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				// We are using the maximum timeout.
				// It is likely that for this webhook handles the download and compilation of the flights wasm.
				// In general this should be fast, on the order of a couple seconds, but lets stay on the side of caution for now.
				TimeoutSeconds: ptr.To(int32(30)),
				MatchPolicy:    ptr.To(admissionregistrationv1.Exact),
				MatchConditions: []admissionregistrationv1.MatchCondition{
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
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"yoke.cd"},
							APIVersions: []string{"v1alpha1"},
							Resources:   []string{"flights", "clusterflights"},
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
							Or(`object.metadata.name != ""`, `oldObject.metadata.name != ""`),
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
							admissionregistrationv1.Create,
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

	typedWebhooks := []*admissionregistrationv1.ValidatingWebhookConfiguration{
		airwayValidation,
		flightValidation,
		resourceValidation,
		externalResourceValidation,
	}

	var webhooks []*unstructured.Unstructured
	for _, webhook := range typedWebhooks {
		webhooks = append(webhooks, internal.Must2(internal.ToUnstructured(webhook)))
	}

	if err := client.ApplyResources(ctx, webhooks, k8s.ApplyResourcesOpts{ApplyOpts: forceful}); err != nil {
		return nil, fmt.Errorf("failed to apply webhooks: %w", err)
	}

	if err := client.WaitForReadyMany(ctx, append(crds, webhooks...), k8s.WaitOptions{Timeout: 30 * time.Second, Interval: time.Second}); err != nil {
		return nil, fmt.Errorf("failed to wait for resources to become ready: %w", err)
	}

	return func(ctx context.Context) error {
		intf := client.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations()
		var errs []error
		for _, webhook := range typedWebhooks {
			if err := intf.Delete(ctx, webhook.Name, metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("%s: %w", webhook.Name, err))
			}
		}
		return xerr.MultiErrFrom("failed to delete validation webhooks", errs...)
	}, nil
}

func And(expressions ...string) string {
	return fmt.Sprintf("(%s)", strings.Join(expressions, " && "))
}

func Or(expressions ...string) string {
	return fmt.Sprintf("(%s)", strings.Join(expressions, " || "))
}
