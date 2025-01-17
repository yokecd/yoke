package tower

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type YamlResult string

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
	Search            textinput.Model
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
		view.Text = string(msg)
		view.Loaded = true
	case tea.KeyMsg:
		if !view.Search.Focused() {
			switch msg.Type {
			case tea.KeyEsc:
				return view.Back.Model(view.Dim), view.Back.Cmd
			}
			switch msg.String() {
			case "/":
				return view, view.Search.Focus()
			case "m":
				view.WithManagedFields = !view.WithManagedFields
			}
		} else {
			switch msg.Type {
			case tea.KeyEsc:
				view.Search.SetValue("")
				view.Search.Blur()
				return view, nil
			case tea.KeyEnter:
				view.Search.Blur()
				return view, nil
			}
		}

	}

	var cmds []tea.Cmd
	var cmd tea.Cmd

	view.Viewport, cmd = view.Viewport.Update(msg)
	cmds = append(cmds, cmd)

	view.Search, cmd = view.Search.Update(msg)
	cmds = append(cmds, cmd)

	text := func() string {
		if view.WithManagedFields {
			return view.Text
		}
		value, _ := removeManagedFields(view.Text)
		return value
	}()

	text = highlightYaml(text)
	text = hightlightSearch(text, view.Search.Value())

	view.Viewport.SetContent(text)

	return view, tea.Batch(cmds...)
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

	content := func() string {
		if view.Err != nil {
			return errorStyle(view.Err.Error())
		}
		if !view.Loaded {
			return fmt.Sprintf("loading %s %s/%s ...", view.Resource.GK, view.Resource.Namespace, view.Resource.Name)
		}

		view.Viewport.Width = view.Dim.Width - 4
		view.Viewport.Height = mainHeight
		return view.Viewport.View()
	}()

	content = border.
		Width(view.Dim.Width - 2).
		Height(mainHeight).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Top, header, search, content)
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

func hightlightSearch(text, search string) string {
	if search == "" {
		return text
	}

	repl := lipgloss.NewStyle().
		Background(lipgloss.Color("#00ffff")).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Render(search)

	var out strings.Builder
	for {
		before, after, ok := strings.Cut(text, search)
		out.WriteString(before)
		if !ok {
			break
		}
		out.WriteString(repl)
		text = after
	}

	return out.String()
}
