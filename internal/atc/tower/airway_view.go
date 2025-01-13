package tower

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
)

type (
	GetAirwayListResult []v1alpha1.Airway
)

type AirwayListView struct {
	tea.Model
}

func MakeAirwayListView(dim tea.WindowSizeMsg) AirwayListView {
	return AirwayListView{
		Model: TableView[v1alpha1.Airway]{
			Dim:     dim,
			Search:  textinput.New(),
			Table:   table.New(),
			Title:   "airways",
			Data:    []v1alpha1.Airway{},
			Columns: []string{"Name", "Status", "Msg"},
			ToRows: func(airways []v1alpha1.Airway) []table.Row {
				rows := make([]table.Row, len(airways))
				for i, airway := range airways {
					rows[i] = table.Row{airway.Name, airway.Status.Status, airway.Status.Msg}

					switch airway.Status.Status {
					case "Error":
						styleRow(lipgloss.NewStyle().Foreground(lipgloss.Color("#900")), rows[i])
					case "InProgress":
						styleRow(lipgloss.NewStyle().Foreground(lipgloss.Color("#990")), rows[i])
					}
				}
				return rows
			},
			Back: nil,
			Forward: func(airway v1alpha1.Airway) Nav {
				gk := schema.GroupKind{
					Group: airway.Spec.Template.Group,
					Kind:  airway.Spec.Template.Names.Kind,
				}
				return Nav{
					Model: func(msg tea.WindowSizeMsg) tea.Model {
						return MakeFlightListView(airway.Spec.Template.Names.Plural, msg)
					},
					Cmd: func() tea.Msg {
						return ExecMsg(func(cmds Commands) tea.Cmd {
							return cmds.GetFlightList(gk)
						})
					},
					Desc: "view flights " + gk.String(),
				}
			},
			Yaml: func(airway v1alpha1.Airway) Nav {
				ref := ResourceRef{
					Name: airway.Name,
					GK:   airway.GroupVersionKind().GroupKind(),
				}
				return Nav{
					Model: func(msg tea.WindowSizeMsg) tea.Model {
						return YamlView{
							Resource: ref,
							Dim:      msg,
							Back: Nav{
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

func (view AirwayListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(GetAirwayListResult); ok {
		msg = TableDataMsg[v1alpha1.Airway](result)
	}

	var cmd tea.Cmd
	view.Model, cmd = view.Model.Update(msg)
	return view, cmd
}

func (view AirwayListView) View() string {
	return view.Model.View()
}
