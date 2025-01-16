package tower

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/internal/atc"
)

type ReadyResource struct {
	Ready *bool
	*unstructured.Unstructured
}

type GetResourcesResult []ReadyResource

type ResourcesView struct {
	tea.Model
}

type PrevRef struct {
	Title   string
	Refresh *RefreshConfig
}

type MakeResourcesViewParams struct {
	Dim    tea.WindowSizeMsg
	Flight unstructured.Unstructured
	Prev   PrevRef
}

func MakeResourcesView(params MakeResourcesViewParams) ResourcesView {
	return ResourcesView{
		Model: TableView[ReadyResource]{
			Err:     nil,
			Dim:     params.Dim,
			Search:  textinput.New(),
			Table:   table.New(),
			Title:   "resources",
			Data:    nil,
			Columns: []string{"Name", "Namespace", "Kind", "Version", "Group", "Ready"},
			ToRows: func(resources []ReadyResource) []table.Row {
				rows := make([]table.Row, len(resources))
				for i, resource := range resources {
					gvk := resource.GroupVersionKind()
					rows[i] = table.Row{
						resource.GetName(),
						resource.GetNamespace(),
						gvk.Kind,
						gvk.Version,
						gvk.Group,
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
			Refresh: &RefreshConfig{
				Func: func() tea.Msg {
					return ExecMsg(func(c Commands) tea.Cmd {
						return c.GetRevisionResources(atc.ReleaseName(&params.Flight), params.Flight.GetNamespace())
					})
				},
			},
			Back: &Nav{
				Model: func(dim tea.WindowSizeMsg) tea.Model {
					return MakeFlightListView(params.Prev.Title, params.Prev.Refresh, dim)
				},
				Cmd: func() tea.Msg {
					return ExecMsg(func(cmds Commands) tea.Cmd {
						return cmds.GetFlightList(params.Flight.GroupVersionKind().GroupKind())
					})
				},
				Desc: "view " + params.Flight.GroupVersionKind().GroupKind().String(),
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
							Search:   textinput.New(),
							Back: Nav{
								Model: func(dim tea.WindowSizeMsg) tea.Model {
									return MakeResourcesView(MakeResourcesViewParams{
										Dim:    dim,
										Flight: params.Flight,
										Prev: PrevRef{
											Title:   params.Prev.Title,
											Refresh: params.Prev.Refresh,
										},
									})
								},
								Cmd: func() tea.Msg {
									return ExecMsg(func(cmds Commands) tea.Cmd {
										return cmds.GetRevisionResources(atc.ReleaseName(&params.Flight), params.Flight.GetNamespace())
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

func (view ResourcesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(GetResourcesResult); ok {
		msg = TableDataMsg[ReadyResource](result)
	}

	var cmd tea.Cmd
	view.Model, cmd = view.Model.Update(msg)

	return view, cmd
}
