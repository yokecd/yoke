package x

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/davidmdm/ansi"
)

var cyan = ansi.MakeStyle(ansi.FgCyan)

type xoptions struct {
	Env []string
	Dir string
}

func Env(e ...string) XOpt {
	return func(opts *xoptions) {
		opts.Env = e
	}
}

func Dir(d string) XOpt {
	return func(opts *xoptions) {
		opts.Dir = d
	}
}

type XOpt func(*xoptions)

func X(line string, opts ...XOpt) error {
	return Xf(line, nil, opts...)
}

func Xf(line string, printArgs []any, opts ...XOpt) error {
	var options xoptions
	for _, apply := range opts {
		apply(&options)
	}

	line = fmt.Sprintf(line, printArgs...)

	args := regexp.MustCompile(`\s+`).Split(line, -1)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), options.Env...)
	cmd.Dir = options.Dir

	cyan.Println(line)
	return cmd.Run()
}
