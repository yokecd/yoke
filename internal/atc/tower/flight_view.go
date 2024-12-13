package tower

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yokecd/yoke/internal/atc"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type GetFlightListResult *unstructured.UnstructuredList

type FlightListView struct {
	tea.Model
}

func MakeFlightListView(dim tea.WindowSizeMsg) FlightListView {
	return FlightListView{
		Model: TableView[unstructured.Unstructured]{
			Dim:     dim,
			Table:   table.New(),
			Search:  textinput.New(),
			Columns: []string{"Name", "Namespace", "Status", "Msg"},
			ToRows: func(resources []unstructured.Unstructured) []table.Row {
				rows := make([]table.Row, len(resources))
				for i, resource := range resources {
					rows[i] = table.Row{
						resource.GetName(),
						resource.GetNamespace(),
						field(&resource, "status", "status"),
						field(&resource, "status", "msg"),
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
				gk := resource.GroupVersionKind().GroupKind()
				return Nav{
					Model: func(msg tea.WindowSizeMsg) tea.Model {
						return MakeRevisionView(msg, gk)
					},
					Cmd: func() tea.Msg {
						return ExecMsg(func(cmds Commands) tea.Cmd {
							name := resource.GetName()
							if namespace := resource.GetNamespace(); namespace != "" {
								name = atc.ReleaseName(&resource)
							}
							return cmds.GetRevisionResources(name)
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
							Dim:      dim,
							Back: Nav{
								Model: func(msg tea.WindowSizeMsg) tea.Model {
									return MakeFlightListView(msg)
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
