package tower

import (
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
)

type (
	GetAirwayListResult []v1alpha1.Airway
)

type AirwayListView struct {
	Table   table.Model
	Airways GetAirwayListResult
	Dim     tea.WindowSizeMsg
	Err     error
}

func (view AirwayListView) Init() tea.Cmd {
	return nil
}

func (view AirwayListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case error:
		view.Err = msg
		return view, nil

	case tea.WindowSizeMsg:
		view.Dim = msg
		return view, nil

	case GetAirwayListResult:
		view.Airways = msg

		var rows []table.Row
		for _, airway := range msg {
			rows = append(rows, table.Row{
				airway.GetName(),
				airway.Status.Status,
				airway.Status.Msg,
			})
		}

		buffer := 3

		view.Table.SetColumns(
			[]table.Column{
				{Title: "Name", Width: maxLenRow(rows, 0, 4+buffer, 30)},
				{Title: "Status", Width: maxLenRow(rows, 1, 6+buffer, 30)},
				{Title: "Msg", Width: maxLenRow(rows, 2, 3+buffer, 30)},
			},
		)
		view.Table.SetRows(rows)
		view.Table.SetHeight(min(len(rows)+1, 21))
		view.Table.Focus()

		s := table.DefaultStyles()

		s.Header = s.Header.Align(lipgloss.Center)

		s.Selected = s.Selected.
			Foreground(color.White).
			Background(color.SelectedFG).
			Bold(false)

		view.Table.SetStyles(s)

		return view, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			row := view.Table.SelectedRow()
			if row == nil {
				return view, nil
			}

			name := row[0]

			ref := ResourceRef{
				Name: name,
				GK:   schema.GroupKind{Group: "yoke.cd", Kind: "Airway"},
			}

			yamlView := YamlView{
				Resource: ref,
				Dim:      view.Dim,
				Back: Nav{
					Model: func(dim tea.WindowSizeMsg) tea.Model {
						return AirwayListView{Dim: dim}
					},
					Cmd: func() tea.Msg {
						return ExecMsg(func(cmds Commands) tea.Cmd { return cmds.GetAirwayList })
					},
				},
				HeaderActions: []HeaderActionItem{
					{
						Key:         "esc",
						Description: "view airways",
					},
				},
			}

			getAirwayResource := func() tea.Msg {
				return ExecMsg(func(cmd Commands) tea.Cmd {
					return cmd.GetResourceYaml(ref)
				})
			}

			return yamlView, getAirwayResource
		}

		switch msg.Type {
		case tea.KeyEnter:
			airway := view.Airways[view.Table.Cursor()]

			gk := schema.GroupKind{
				Group: airway.Spec.Template.Group,
				Kind:  airway.Spec.Template.Names.Kind,
			}

			flightsView := FlightListView{Dim: view.Dim, GK: gk}

			cmd := func() tea.Msg {
				return ExecMsg(func(cmds Commands) tea.Cmd {
					return cmds.GetFlightList(gk)
				})
			}

			return flightsView, cmd
		}
	}

	var cmd tea.Cmd
	view.Table, cmd = view.Table.Update(msg)
	return view, cmd
}

func (view AirwayListView) View() string {
	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		banner,
		HeaderActionItems{
			{Key: "y", Description: "yaml"},
			{Key: "enter", Description: "view flights"},
		}.String(),
	)

	content := func() string {
		if view.Err != nil {
			return errorStyle(view.Err.Error())
		}
		return view.Table.View()
	}()

	content = border.
		Width(view.Dim.Width - 2).
		Height(view.Dim.Height - lipgloss.Height(header) - 2).
		Render(content)

	return lipgloss.JoinVertical(
		lipgloss.Top,
		header,
		content,
	)
}

var _ tea.Model = AirwayListView{}
