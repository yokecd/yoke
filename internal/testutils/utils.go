package testutils

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	var options xoptions
	for _, apply := range opts {
		apply(&options)
	}

	args := regexp.MustCompile(`\s+`).Split(line, -1)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), options.Env...)
	cmd.Dir = options.Dir

	cyan.Println(line)
	return cmd.Run()
}

func JsonReader(value any) io.Reader {
	data, err := json.Marshal(value)
	if err != nil {
		pr, pw := io.Pipe()
		pw.CloseWithError(err)
		return pr
	}
	return bytes.NewReader(data)
}

func EventuallyNoErrorf(t *testing.T, fn func() error, tick time.Duration, timeout time.Duration, msg string, args ...any) {
	t.Helper()

	var (
		ticker   = time.NewTimer(0)
		deadline = time.Now().Add(timeout)
	)

	for range ticker.C {
		err := fn()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			require.NoErrorf(t, err, msg, args...)
			return
		}
		ticker.Reset(tick)
	}
}
