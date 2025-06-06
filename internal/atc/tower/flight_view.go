package tower

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/atc"
)

type GetFlightListResult *unstructured.UnstructuredList

type FlightListView struct {
	tea.Model
}

func MakeFlightListView(title string, refresh *RefreshConfig, dim tea.WindowSizeMsg) FlightListView {
	return FlightListView{
		Model: TableView[unstructured.Unstructured]{
			Dim:     dim,
			Table:   table.New(),
			Title:   title,
			Search:  textinput.New(),
			Refresh: refresh,
			Columns: []string{"Name", "Namespace", "Status", "Msg"},
			ToRows: func(resources []unstructured.Unstructured) []table.Row {
				rows := make([]table.Row, len(resources))
				for i, resource := range resources {
					readyCondition := internal.GetFlightReadyCondition(&resource)
					if readyCondition == nil {
						continue
					}
					rows[i] = table.Row{
						resource.GetName(),
						resource.GetNamespace(),
						readyCondition.Reason,
						readyCondition.Message,
					}
				}
				return rows
			},
			Back: &Nav{
				Model: func(msg tea.WindowSizeMsg) tea.Model {
					return MakeAirwayListView(msg)
				},
				Cmd: func() tea.Msg {
					return ExecMsg(func(cmds Commands) tea.Cmd {
						return cmds.GetAirwayList
					})
				},
				Desc: "view airways",
			},
			Forward: func(resource unstructured.Unstructured) Nav {
				return Nav{
					Model: func(msg tea.WindowSizeMsg) tea.Model {
						return MakeResourcesView(MakeResourcesViewParams{
							Dim:    msg,
							Flight: resource,
							Prev: PrevRef{
								Title:   title,
								Refresh: refresh,
							},
						})
					},
					Cmd: func() tea.Msg {
						return ExecMsg(func(cmds Commands) tea.Cmd {
							name := resource.GetName()
							if namespace := resource.GetNamespace(); namespace != "" {
								name = atc.ReleaseName(&resource)
							}
							return cmds.GetRevisionResources(name, resource.GetNamespace())
						})
					},
					Desc: "view resources",
				}
			},
			Yaml: func(resource unstructured.Unstructured) Nav {
				ref := ResourceRef{
					Name:      resource.GetName(),
					Namespace: resource.GetNamespace(),
					GK:        resource.GroupVersionKind().GroupKind(),
				}
				return Nav{
					Model: func(msg tea.WindowSizeMsg) tea.Model {
						return YamlView{
							Resource: ref,
							Search:   textinput.New(),
							Dim:      dim,
							Back: Nav{
								Model: func(msg tea.WindowSizeMsg) tea.Model {
									return MakeFlightListView(title, refresh, msg)
								},
								Cmd: func() tea.Msg {
									return ExecMsg(func(cmds Commands) tea.Cmd {
										return cmds.GetFlightList(ref.GK)
									})
								},
								Desc: "view " + ref.GK.String(),
							},
						}
					},
					Cmd: func() tea.Msg {
						return ExecMsg(func(cmds Commands) tea.Cmd {
							return cmds.GetResourceYaml(ref)
						})
					},
					Desc: "view yaml",
				}
			},
		},
	}
}

func (view FlightListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(GetFlightListResult); ok {
		msg = TableDataMsg[unstructured.Unstructured](result.Items)
	}

	var cmd tea.Cmd
	view.Model, cmd = view.Model.Update(msg)

	return view, cmd
}

func (view FlightListView) View() string {
	return view.Model.View()
}
