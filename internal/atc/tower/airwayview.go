package tower

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yokecd/yoke/pkg/apis/airway/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type (
	GetAirwayListResult []v1alpha1.Airway
	GetAirwayResult     string
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
			Foreground(lipgloss.Color("#fff")).
			Background(lipgloss.Color("#5aa")).
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

			airwayView := AirwayView{
				Name: name,
				Dim:  view.Dim,
			}

			getAirway := func() tea.Msg {
				return ExecMsg(func(cmd Commands) tea.Cmd {
					return cmd.GetAirway(name)
				})
			}

			return airwayView, getAirway
		}

		switch msg.Type {
		case tea.KeyEnter:
			next := FlightListView{Dim: view.Dim}

			airway := view.Airways[view.Table.Cursor()]

			cmd := func() tea.Msg {
				return ExecMsg(func(cmds Commands) tea.Cmd {
					return cmds.GetFlightList(schema.GroupKind{
						Group: airway.Spec.Template.Group,
						Kind:  airway.Spec.Template.Names.Kind,
					})
				})
			}

			return next, cmd
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

type AirwayView struct {
	Name     string
	loaded   bool
	viewport viewport.Model
	Err      error
	Dim      tea.WindowSizeMsg
}

func (a AirwayView) Init() tea.Cmd {
	return nil
}

func (a AirwayView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.Dim = msg
		return a, nil
	case error:
		a.Err = msg
		return a, nil
	case GetAirwayResult:
		a.viewport.SetContent(highlightYaml(string(msg)))
		a.loaded = true
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return AirwayListView{Dim: a.Dim}, func() tea.Msg {
				return ExecMsg(func(cmd Commands) tea.Cmd { return cmd.GetAirwayList })
			}
		}
	}

	var cmd tea.Cmd
	a.viewport, cmd = a.viewport.Update(msg)

	return a, cmd
}

func (a AirwayView) View() string {
	header := lipgloss.JoinHorizontal(lipgloss.Top, banner, HeaderActionItems{
		{Key: "esc", Description: "view airways"},
	}.String())

	content := func() string {
		if a.Err != nil {
			return errorStyle(a.Err.Error())
		}
		if !a.loaded {
			return "loading airway..."
		}

		a.viewport.Width = a.Dim.Width - 4
		a.viewport.Height = a.Dim.Height - lipgloss.Height(header) - 2
		return a.viewport.View()
	}()

	content = border.
		Width(a.Dim.Width - 2).
		Height(a.Dim.Height - lipgloss.Height(header) - 2).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Top, header, content)
}

var _ tea.Model = AirwayView{}
