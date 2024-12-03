package tower

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type (
	YamlResult string
)

type Nav struct {
	Model func(tea.WindowSizeMsg) tea.Model
	Cmd   tea.Cmd
}

type ResourceRef struct {
	Name      string
	Namespace string
	GK        schema.GroupKind
}

type YamlView struct {
	Resource      ResourceRef
	Loaded        bool
	Viewport      viewport.Model
	Err           error
	Dim           tea.WindowSizeMsg
	Back          Nav
	HeaderActions HeaderActionItems
}

func (view YamlView) Init() tea.Cmd {
	return nil
}

func (view YamlView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		view.Dim = msg
		return view, nil
	case error:
		view.Err = msg
		return view, nil
	case YamlResult:
		view.Viewport.SetContent(highlightYaml(string(msg)))
		view.Loaded = true
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return view.Back.Model(view.Dim), view.Back.Cmd
		}
	}

	var cmd tea.Cmd
	view.Viewport, cmd = view.Viewport.Update(msg)

	return view, cmd
}

func (view YamlView) View() string {
	header := lipgloss.JoinHorizontal(lipgloss.Top, banner, view.HeaderActions.String())

	content := func() string {
		if view.Err != nil {
			return errorStyle(view.Err.Error())
		}
		if !view.Loaded {
			return fmt.Sprintf("loading %s %s/%s ...", view.Resource.GK, view.Resource.Namespace, view.Resource.Name)
		}

		view.Viewport.Width = view.Dim.Width - 4
		view.Viewport.Height = view.Dim.Height - lipgloss.Height(header) - 2
		return view.Viewport.View()
	}()

	content = border.
		Width(view.Dim.Width - 2).
		Height(view.Dim.Height - lipgloss.Height(header) - 2).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Top, header, content)
}

var _ tea.Model = YamlView{}
