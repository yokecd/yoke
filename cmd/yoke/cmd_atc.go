package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	spinner.Pulse.FPS = time.Second / 3
}

var cyan = lipgloss.Color("#00ffff")

var debugFile *os.File

func init() {
	debugFile, _ = os.OpenFile("./debug.txt", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if _, err := debugFile.WriteString("DEBUG.txt\n\n"); err != nil {
		panic(err)
	}
}

type Commands struct {
	GetAirways tea.Cmd
}

type ATCDashboard struct {
	Base        lipgloss.Style
	LoadSpinner spinner.Model
	Airways     *unstructured.UnstructuredList
	Table       table.Model
	Err         error
	Commands
}

func (dashboard ATCDashboard) Init() tea.Cmd {
	return tea.Batch(dashboard.LoadSpinner.Tick, dashboard.GetAirways)
}

func (dashboard ATCDashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	fmt.Fprintf(debugFile, "%#v\n\n", msg)
	debugFile.Sync()

	if err, ok := msg.(error); ok {
		dashboard.Err = err
		return dashboard, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// dashboard.Base = dashboard.Base.Height(msg.Height - 4)
		dashboard.Base = dashboard.Base.Width(msg.Width - 4)
		// dashboard.Table.SetWidth(msg.Width - 4)
		return dashboard, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return dashboard, tea.Quit
		case "ctrl+z":
			return dashboard, tea.Suspend
		}
	case GetAirwayResult:
		dashboard.Airways = msg

		var rows []table.Row
		for _, airway := range msg.Items {
			rows = append(rows, table.Row{
				airway.GetName(),
				field(&airway, "status", "status"),
				field(&airway, "status", "msg"),
			})
		}

		buffer := 3

		dashboard.Table.SetColumns(
			[]table.Column{
				{Title: "Name", Width: maxLenRow(rows, 0, 4+buffer, 30)},
				{Title: "Status", Width: maxLenRow(rows, 1, 6+buffer, 30)},
				{Title: "Msg", Width: maxLenRow(rows, 2, 3+buffer, 30)},
			},
		)
		dashboard.Table.SetRows(rows)
		dashboard.Table.SetHeight(min(len(rows)+1, 21))
		dashboard.Table.Focus()

		s := table.DefaultStyles()

		s.Header = s.Header.
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#000000")).
			BorderBottom(true).
			Bold(false).
			Align(lipgloss.Center)

		s.Selected = s.Selected.
			Foreground(lipgloss.Color("#fff")).
			Background(lipgloss.Color("#57f")).
			Bold(false)

		dashboard.Table.SetStyles(s)

		return dashboard, nil

	}

	var cmds []tea.Cmd
	var cmd tea.Cmd

	dashboard.LoadSpinner, cmd = dashboard.LoadSpinner.Update(msg)
	cmds = append(cmds, cmd)

	dashboard.Table, cmd = dashboard.Table.Update(msg)
	cmds = append(cmds, cmd)

	return dashboard, tea.Batch(cmds...)
}

func (dashboard ATCDashboard) View() string {
	if dashboard.Err != nil {
		return "ERROR:" + dashboard.Err.Error()
	}

	if dashboard.Airways != nil {
		return dashboard.Base.Render(dashboard.Table.View()) + "\n" + dashboard.Table.HelpView()
	}

	return fmt.Sprintf("%s loading airways...\n", dashboard.LoadSpinner.View())
}

var _ tea.Model = ATCDashboard{}

func ATC(ctx context.Context) error {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to initialize kube client: %w", err)
	}

	baseStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	app := tea.NewProgram(
		ATCDashboard{
			Base: baseStyle,
			LoadSpinner: spinner.New(
				spinner.WithSpinner(spinner.Pulse),
				spinner.WithStyle(lipgloss.NewStyle().Foreground(cyan)),
			),
			Table: table.New(),
			Commands: Commands{
				GetAirways: func() tea.Msg {
					mapping, err := client.Mapper.RESTMapping(schema.GroupKind{Group: "yoke.cd", Kind: "Airway"})
					if err != nil {
						return fmt.Errorf("failed to get airway mappinng: %w", err)
					}

					airways, err := client.Dynamic.Resource(mapping.Resource).List(ctx, metav1.ListOptions{})
					if err != nil {
						return fmt.Errorf("failed to get airways: %w", err)
					}

					return GetAirwayResult(airways)
				},
			},
		},
		tea.WithAltScreen(),
	)

	if _, err := app.Run(); err != nil {
		return fmt.Errorf("failed to run app: %w", err)
	}

	return nil
}

type GetAirwayResult *unstructured.UnstructuredList

func maxLenRow(rows []table.Row, index int, minLim, maxLim int) int {
	var longest int
	for _, row := range rows {
		if length := len(row[index]); length > longest {
			longest = length
		}
	}

	longest = min(longest, maxLim)
	longest = max(longest, minLim)

	return longest
}

func field(resource *unstructured.Unstructured, keys ...string) string {
	value, _, _ := unstructured.NestedFieldCopy(resource.Object, keys...)
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%v", value)
}
