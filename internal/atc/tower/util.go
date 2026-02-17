package tower

import (
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

func maxLenRow(rows []table.Row, index int, minLim, maxLim int) int {
	var longest int
	for _, row := range rows {
		if index >= len(row) {
			continue
		}
		if length := lipgloss.Width(row[index]); length > longest {
			longest = length
		}
	}

	longest = min(longest, maxLim)
	longest = max(longest, minLim)

	return longest
}

func styleRow(style lipgloss.Style, row table.Row) {
	for i, value := range row {
		row[i] = style.Render(value)
	}
}

func highlightYaml(text string) string {
	var builder strings.Builder
	if err := quick.Highlight(&builder, text, "yaml", "terminal", "monokai"); err != nil {
		return text
	}
	return builder.String()
}

var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f00")).Render

type HeaderActionItem struct {
	Key         string
	Description string
}

type HeaderActionItems []HeaderActionItem

func (items HeaderActionItems) String() string {
	var keys string
	var description strings.Builder
	var maxKeyLen int
	for i, item := range items {
		if i != 0 {
			keys += "\n"
			description.WriteString("\n")
		}

		keys += "<" + item.Key + ">"
		description.WriteString(item.Description)

		if length := len(item.Key) + 2; length > maxKeyLen {
			maxKeyLen = length
		}

	}

	keys = lipgloss.NewStyle().Width(maxKeyLen + 2).Foreground(lipgloss.Color("#7266ee")).Render(keys)
	content := lipgloss.JoinHorizontal(lipgloss.Top, keys, description.String())

	return lipgloss.NewStyle().MarginLeft(3).Render(content)
}
