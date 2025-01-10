package tower

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var banner = cyanSyle.Render(`     _   _      _____                      
    / \ | |_ __|_   _|____      _____ _ __ 
   / _ \| __/ __|| |/ _ \ \ /\ / / _ \ '__|
  / ___ \ || (__ | | (_) \ V  V /  __/ |   
 /_/   \_\__\___||_|\___/ \_/\_/ \___|_|   `)

var debugFile *os.File

func debugf(format string, args ...any) {
	if debugFile == nil {
		return
	}
	_, _ = fmt.Fprintf(debugFile, format+"\n", args...)
}

func SetupDebugFile(dst string) (err error) {
	debugFile, err = os.OpenFile(dst, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(debugFile, "debug session start: %s\n\n", time.Now().Format(time.RFC3339Nano))
	return err
}

type Commands struct {
	GetAirwayList        tea.Cmd
	GetResourceYaml      func(ref ResourceRef) tea.Cmd
	GetFlightList        func(gk schema.GroupKind) tea.Cmd
	GetRevisionResources func(name, ns string) tea.Cmd
}

type ATCDashboard struct {
	Content tea.Model
	Commands
}

func (dashboard ATCDashboard) Init() tea.Cmd {
	return tea.Batch(dashboard.GetAirwayList)
}

func (dashboard ATCDashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	debugf("%v", func() any {
		if value, ok := msg.(fmt.Stringer); ok {
			return value.String()
		}
		return msg
	}())

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return dashboard, tea.Quit
		case tea.KeyCtrlZ:
			return dashboard, tea.Suspend
		}

	case ExecMsg:
		return dashboard, msg(dashboard.Commands)
	}

	var cmds []tea.Cmd
	var cmd tea.Cmd

	dashboard.Content, cmd = dashboard.Content.Update(msg)
	cmds = append(cmds, cmd)

	return dashboard, tea.Batch(cmds...)
}

func (dashboard ATCDashboard) View() string {
	return dashboard.Content.View()
}

var _ tea.Model = ATCDashboard{}

type ExecMsg func(Commands) tea.Cmd

type Header struct {
	Width int
}

func (h Header) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (h Header) Update(tea.Msg) (tea.Model, tea.Cmd) {
	panic("unimplemented")
}

// View implements tea.Model.
func (h Header) View() string {
	return border.Width(h.Width).Foreground(color.Cyan).Render()
}

var _ tea.Model = Header{}

var border = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	Padding(0, 1)
