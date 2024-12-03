package tower

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type GetFlightListResult *unstructured.UnstructuredList

type FlightListView struct {
	Err    error
	Loaded bool
	Dim    tea.WindowSizeMsg
	Table  table.Model
}

func (view FlightListView) Init() tea.Cmd { return nil }

func (view FlightListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		view.Dim = msg
		return view, nil

	case error:
		view.Err = msg
		return view, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return AirwayListView{Dim: view.Dim}, func() tea.Msg {
				return ExecMsg(func(commands Commands) tea.Cmd { return commands.GetAirwayList })
			}
		}

	case GetFlightListResult:
		var rows []table.Row
		for _, flight := range msg.Items {
			rows = append(rows, table.Row{
				flight.GetName(),
				flight.GetNamespace(),
				field(&flight, "status", "status"),
				field(&flight, "status", "msg"),
			})
		}

		buffer := 3

		view.Table.SetColumns(
			[]table.Column{
				{Title: "Name", Width: maxLenRow(rows, 0, 4+buffer, 30)},
				{Title: "Namespace", Width: maxLenRow(rows, 1, 9+buffer, 30)},
				{Title: "Status", Width: maxLenRow(rows, 2, 6+buffer, 30)},
				{Title: "Msg", Width: maxLenRow(rows, 3, 3+buffer, 30)},
			},
		)
		view.Table.SetRows(rows)
		view.Table.SetHeight(min(len(rows)+1, 21))
		view.Table.SetWidth(view.Dim.Width - 2)
		view.Table.Focus()

		s := table.DefaultStyles()

		s.Header = s.Header.Align(lipgloss.Center)

		s.Selected = s.Selected.
			Foreground(lipgloss.Color("#fff")).
			Background(lipgloss.Color("#5aa")).
			Bold(false)

		view.Table.SetStyles(s)
		view.Table.KeyMap = table.DefaultKeyMap()

		view.Loaded = true

		return view, nil
	}

	var cmd tea.Cmd
	fmt.Fprintf(debugFile, "sending to table: %v (table is %v)\n", msg, view.Table.Focused())
	view.Table, cmd = view.Table.Update(msg)

	return view, cmd
}

func (view FlightListView) View() string {
	content := func() string {
		if view.Err != nil {
			return errorStyle(view.Err.Error())
		}

		if !view.Loaded {
			return "loading flight..."
		}

		view.Table.SetHeight(view.Dim.Height - lipgloss.Height(banner) - 2 - 1)

		return view.Table.View() + view.Table.HelpView()
	}()

	header := lipgloss.JoinHorizontal(lipgloss.Top, banner, HeaderActionItems{
		{Key: "esc", Description: "view airways"},
	}.String())

	content = border.Width(view.Dim.Width - lipgloss.Height(header) - 2).Render(content)

	return lipgloss.JoinVertical(lipgloss.Top, header, content)
}

var _ tea.Model = FlightListView{}
