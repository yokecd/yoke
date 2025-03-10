package internal

import (
	"slices"
	"strings"

	"github.com/davidmdm/ansi"
)

func CutArgs(args []string) ([]string, []string) {
	idx := slices.Index(args, "--")
	if idx == -1 {
		return args, nil
	}
	return args[:idx], args[idx+1:]
}

var (
	cyan    = ansi.MakeStyle(ansi.FgCyan)
	yellow  = ansi.MakeStyle(ansi.FgYellow)
	magenta = ansi.MakeStyle(ansi.FgMagenta)
)

func Colorize(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if len(line) == 0 || line[0] != '!' {
			continue
		}

		color, line, _ := strings.Cut(line, " ")
		switch color {
		case "!cyan":
			lines[i] = cyan.Sprint(line)
		case "!yellow":
			lines[i] = yellow.Sprint(line)
		case "!magenta":
			lines[i] = magenta.Sprint(line)
		default:
			lines[i] = line
		}
	}
	return strings.Join(lines, "\n")
}
