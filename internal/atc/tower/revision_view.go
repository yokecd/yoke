package tower

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ReadyResource struct {
	Ready *bool
	*unstructured.Unstructured
}

type GetRevisionResult []ReadyResource

type RevisionView struct {
	tea.Model
}

func MakeRevisionView(dim tea.WindowSizeMsg, flightGK schema.GroupKind) RevisionView {
	return RevisionView{
		Model: TableView[ReadyResource]{
			Err:     nil,
			Dim:     dim,
			Search:  textinput.New(),
			Table:   table.New(),
			Data:    nil,
			Columns: []string{"Name", "Namespace", "GVK", "Ready"},
			ToRows: func(resources []ReadyResource) []table.Row {
				rows := make([]table.Row, len(resources))
				for i, resource := range resources {
					rows[i] = table.Row{
						resource.GetName(),
						resource.GetNamespace(),
						resource.GroupVersionKind().String(),
						func() string {
							if resource.Ready == nil {
								return "n/a"
							}
							return strconv.FormatBool(*resource.Ready)
						}(),
					}
				}

				return rows
			},
			Back: &Nav{
				Model: func(dim tea.WindowSizeMsg) tea.Model {
					return MakeFlightListView(dim)
				},
				Cmd: func() tea.Msg {
					return ExecMsg(func(cmds Commands) tea.Cmd {
						return cmds.GetFlightList(flightGK)
					})
				},
				Desc: "view " + flightGK.String(),
			},
			Forward: nil,
			Yaml: func(resource ReadyResource) Nav {
				ref := ResourceRef{
					Name:      resource.GetName(),
					Namespace: resource.GetNamespace(),
					GK:        resource.GroupVersionKind().GroupKind(),
				}
				return Nav{
					Model: func(dim tea.WindowSizeMsg) tea.Model {
						return YamlView{
							Resource: ref,
							Viewport: viewport.Model{},
							Dim:      dim,
							Back: Nav{
								Model: func(dim tea.WindowSizeMsg) tea.Model {
									return MakeRevisionView(dim, flightGK)
								},
								Cmd: func() tea.Msg {
									return ExecMsg(func(cmds Commands) tea.Cmd {
										name := ref.Name
										if ref.Namespace != "" {
											name = ref.Namespace + "-" + name
										}
										return cmds.GetRevisionResources(name)
									})
								},
								Desc: "view release",
							},
							WithManagedFields: false,
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

func (view RevisionView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(GetRevisionResult); ok {
		msg = TableDataMsg[ReadyResource](result)
	}

	var cmd tea.Cmd
	view.Model, cmd = view.Model.Update(msg)

	return view, cmd
}
