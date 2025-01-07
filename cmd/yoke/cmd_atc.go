package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/yokecd/yoke/internal/atc/tower"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
)

func ATC(ctx context.Context) error {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to initialize kube client: %w", err)
	}

	app := tea.NewProgram(
		tower.ATCDashboard{
			Content: tower.MakeAirwayListView(tea.WindowSizeMsg{}),
			Commands: tower.Commands{
				GetAirwayList: func() tea.Msg {
					mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: "yoke.cd", Kind: "Airway"})
					if err != nil {
						return fmt.Errorf("failed to get airway mappinng: %w", err)
					}

					list, err := client.Dynamic.Resource(mapping.Resource).List(ctx, metav1.ListOptions{})
					if err != nil {
						return fmt.Errorf("failed to get airways: %w", err)
					}

					airways := make([]v1alpha1.Airway, len(list.Items))
					for i, resource := range list.Items {
						if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, &airways[i]); err != nil {
							return fmt.Errorf("failed to parse airway: %w", err)
						}
					}

					return tower.GetAirwayListResult(airways)
				},

				GetResourceYaml: func(ref tower.ResourceRef) tea.Cmd {
					return func() tea.Msg {
						mapping, err := client.Mapper.RESTMapping(ref.GK)
						if err != nil {
							return fmt.Errorf("failed to get airway mappinng: %w", err)
						}

						intf := func() dynamic.ResourceInterface {
							if mapping.Scope == meta.RESTScopeRoot {
								return client.Dynamic.Resource(mapping.Resource)
							}
							return client.Dynamic.Resource(mapping.Resource).Namespace(ref.Namespace)
						}()

						resource, err := intf.Get(ctx, ref.Name, metav1.GetOptions{})
						if err != nil {
							return fmt.Errorf("failed to get airways: %w", err)
						}

						data, err := yaml.Marshal(resource.Object)
						if err != nil {
							return fmt.Errorf("failed to marshal airway to yaml: %w", err)
						}

						return tower.YamlResult(string(data))
					}
				},

				GetFlightList: func(gk schema.GroupKind) tea.Cmd {
					return func() tea.Msg {
						mapping, err := client.Mapper.RESTMapping(gk)
						if err != nil {
							return fmt.Errorf("failed to lookup mapping for %s: %w", gk, err)
						}

						intf := client.Dynamic.Resource(mapping.Resource)

						if mapping.Scope == meta.RESTScopeRoot {
							resources, err := intf.List(ctx, metav1.ListOptions{})
							if err != nil {
								return fmt.Errorf("failed to list %s: %w", gk, err)
							}
							return tower.GetFlightListResult(resources)
						}

						namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
						if err != nil {
							return fmt.Errorf("failed to list namespaces: %w", err)
						}

						result := new(unstructured.UnstructuredList)

						for _, ns := range namespaces.Items {
							resources, err := intf.Namespace(ns.Name).List(ctx, metav1.ListOptions{})
							if err != nil {
								return fmt.Errorf("failed to list %s/%s: %w", ns.Name, gk, err)
							}
							result.Items = append(result.Items, resources.Items...)
						}

						return tower.GetFlightListResult(result)
					}
				},

				GetRevisionResources: func(name, ns string) tea.Cmd {
					return func() tea.Msg {
						release, err := client.GetRelease(ctx, name, ns)
						if err != nil {
							return fmt.Errorf("failed to get revisions for %q: %w", name, err)
						}

						resources, err := client.GetRevisionResources(ctx, release.ActiveRevision())
						if err != nil {
							return fmt.Errorf("failed to get active resources for %q: %w", name, err)
						}

						var wg sync.WaitGroup
						wg.Add(len(resources))

						semaphore := make(chan struct{}, runtime.NumCPU())

						result := make(tower.GetRevisionResult, len(resources))
						for i, resource := range resources {
							go func() {
								defer wg.Done()

								defer func() { <-semaphore }()
								semaphore <- struct{}{}

								result[i].Unstructured = resource
								ready, err := client.IsReady(ctx, resource)
								if err != nil {
									result[i].Ready = nil
									return
								}
								result[i].Ready = &ready
							}()
						}

						wg.Wait()

						return result
					}
				},
			},
		},
		tea.WithAltScreen(),
	)

	if _, err := app.Run(); err != nil {
		return fmt.Errorf("failed to run app: %w", err)
	}

	return nil
}
