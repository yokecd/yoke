package tower

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type (
	YamlResult string
)

type Nav struct {
	Model func(tea.WindowSizeMsg) tea.Model
	Cmd   tea.Cmd
	Desc  string
}

type ResourceRef struct {
	Name      string
	Namespace string
	GK        schema.GroupKind
}

type YamlView struct {
	Resource          ResourceRef
	Loaded            bool
	Viewport          viewport.Model
	Err               error
	Dim               tea.WindowSizeMsg
	Back              Nav
	Text              string
	WithManagedFields bool
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
		text, _ := removeManagedFields(string(msg))
		view.Text = string(msg)
		view.Viewport.SetContent(highlightYaml(text))
		view.Loaded = true
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return view.Back.Model(view.Dim), view.Back.Cmd
		}

		if msg.String() == "m" {
			view.WithManagedFields = !view.WithManagedFields

			if view.WithManagedFields {
				view.Viewport.SetContent(highlightYaml(view.Text))
			} else {
				next, err := removeManagedFields(view.Text)
				if err != nil {
					view.Err = err
				} else {
					view.Viewport.SetContent(highlightYaml(next))
				}
			}
		}

	}

	var cmd tea.Cmd
	view.Viewport, cmd = view.Viewport.Update(msg)

	return view, cmd
}

func (view YamlView) View() string {
	actions := HeaderActionItems{
		{
			Key:         "esc",
			Description: view.Back.Desc,
		},
		{
			Key:         "m",
			Description: "toggle managed fields",
		},
	}

	header := lipgloss.JoinHorizontal(lipgloss.Top, banner, actions.String())

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

func removeManagedFields(text string) (string, error) {
	var obj map[string]any
	if err := yaml.Unmarshal([]byte(text), &obj); err != nil {
		return text, err
	}

	unstructured.RemoveNestedField(obj, "metadata", "managedFields")

	data, err := yaml.Marshal(obj)
	if err != nil {
		return text, err
	}

	return string(data), nil
}
