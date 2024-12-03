package tower

import "github.com/charmbracelet/lipgloss"

var color = struct {
	Cyan       lipgloss.Color
	White      lipgloss.Color
	SelectedFG lipgloss.Color
}{
	Cyan:       "#0ff",
	White:      "#fff",
	SelectedFG: "#287",
}

var cyanSyle = lipgloss.NewStyle().Foreground(color.Cyan)
