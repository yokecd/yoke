package tower

import (
	"cmp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	gloss "github.com/yokecd/lipgloss"
)

type RefreshConfig struct {
	Func     func() tea.Msg
	Interval time.Duration
}

type TableView[T any] struct {
	Err     error
	Dim     tea.WindowSizeMsg
	Search  textinput.Model
	Table   table.Model
	Data    []T
	Columns []string
	Title   string
	ToRows  func([]T) []table.Row
	Refresh *RefreshConfig
	Back    *Nav
	Forward func(T) Nav
	Yaml    func(T) Nav
}

func (view TableView[T]) Init() tea.Cmd {
	return nil
}

func (view TableView[T]) rows() ([]table.Row, []T) {
	if view.Search.Value() == "" {
		return view.ToRows(view.Data), view.Data
	}

	var (
		rows   []table.Row
		subset []T
	)

outer:
	for i, row := range view.ToRows(view.Data) {
		for _, value := range row {
			if strings.Contains(value, view.Search.Value()) {
				rows = append(rows, row)
				subset = append(subset, view.Data[i])
				continue outer
			}
		}
	}

	return rows, subset
}

func (view TableView[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case error:
		view.Err = msg
		if view.Refresh != nil {
			return view, tea.Tick(
				cmp.Or(view.Refresh.Interval, time.Second),
				func(t time.Time) tea.Msg {
					return view.Refresh.Func()
				},
			)
		}
		return view, nil
	case tea.WindowSizeMsg:
		view.Dim = msg
		return view, nil
	case TableDataMsg[T]:
		view.Err = nil

		if msg == nil {
			// the data comes in as nil but we use nil to signal that we are in a loading state.
			// This allows us to signal empty instead of loading.
			msg = TableDataMsg[T]{}
		}

		view.Data = msg

		rows, _ := view.rows()

		columns := make([]table.Column, len(view.Columns))
		for i, value := range view.Columns {
			columns[i] = table.Column{
				Title: value,
				Width: maxLenRow(rows, i, len(value), 50),
			}
		}

		view.Table.SetColumns(columns)
		view.Table.SetRows(rows)
		view.Table.Focus()
		view.Search.Blur()

		style := table.DefaultStyles()

		style.Selected = style.Selected.
			Foreground(color.White).
			Background(color.SelectedFG).
			Bold(false)

		view.Table.SetStyles(style)

		if view.Refresh != nil {
			return view, tea.Tick(
				cmp.Or(view.Refresh.Interval, time.Second),
				func(t time.Time) tea.Msg {
					return view.Refresh.Func()
				},
			)
		}

		return view, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			if view.Search.Focused() {
				view.Search.Blur()
				view.Search.SetValue("")
				view.Table.Focus()

				rows, _ := view.rows()
				view.Table.SetRows(rows)

				return view, nil
			}
			if view.Back != nil {
				return view.Back.Model(view.Dim), view.Back.Cmd
			}
		case tea.KeyEnter:
			if view.Search.Focused() {
				view.Search.Blur()
				view.Table.Focus()
				return view, nil
			}

			if view.Forward != nil {
				_, data := view.rows()
				if len(data) == 0 {
					return view, nil
				}

				nav := view.Forward(data[view.Table.Cursor()])
				return nav.Model(view.Dim), nav.Cmd
			}
		}
		switch msg.String() {
		case "y", "Y":
			if !view.Search.Focused() && view.Yaml != nil {
				nav := view.Yaml(view.Data[view.Table.Cursor()])
				return nav.Model(view.Dim), nav.Cmd
			}
		case "/":
			if !view.Search.Focused() {
				view.Search.Focus()
				view.Table.Blur()
				return view, nil
			}
		}
	}

	var cmd tea.Cmd
	var cmds []tea.Cmd

	view.Search, cmd = view.Search.Update(msg)
	cmds = append(cmds, cmd)

	if view.Search.Focused() {
		rows, _ := view.rows()
		view.Table.SetRows(rows)
	}

	view.Table, cmd = view.Table.Update(msg)
	cmds = append(cmds, cmd)

	return view, tea.Batch(cmds...)
}

func (view TableView[T]) View() string {
	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		banner,
		func() string {
			actions := HeaderActionItems{}
			if view.Back != nil {
				actions = append(actions, HeaderActionItem{
					Key:         "esc",
					Description: view.Back.Desc,
				})
			}
			var zero T
			if view.Forward != nil {
				actions = append(actions, HeaderActionItem{
					Key:         "enter",
					Description: view.Forward(zero).Desc,
				})
			}
			if view.Yaml != nil {
				actions = append(actions, HeaderActionItem{
					Key:         "y",
					Description: "view yaml",
				})
			}

			return actions.String()
		}(),
	)

	search := func() string {
		if !view.Search.Focused() {
			return ""
		}

		view.Search.Prompt = ""
		view.Search.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef868e"))
		view.Search.PromptStyle = view.Search.TextStyle

		return border.
			BorderTitle(yellow.Render("search")).
			Width(view.Dim.Width - 2).
			Render(view.Search.View())
	}()

	mainHeight := view.Dim.Height - lipgloss.Height(header) - lipgloss.Height(search) - 2

	main := func() string {
		if view.Err != nil {
			return errorStyle(view.Err.Error())
		}
		if view.Data == nil {
			return "loading..."
		}
		if len(view.Data) == 0 {
			if view.Refresh != nil {
				return "nothing found (refreshing in background periodically)"
			}
			return "nothing found"
		}
		view.Table.SetHeight(mainHeight - 1)
		return view.Table.View() + "\n" + view.Table.HelpView()
	}()

	main = border.
		BorderTitle(yellow.Render(view.Title)).
		Width(view.Dim.Width - 2).
		Height(mainHeight).
		Render(main)

	return lipgloss.JoinVertical(lipgloss.Top, header, search, main)
}

var _ tea.Model = TableView[any]{}

type TableDataMsg[T any] []T

var yellow = gloss.NewStyle().Foreground(gloss.Color("#ff0"))
