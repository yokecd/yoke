package testutils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func JsonReader(value any) io.Reader {
	data, err := json.Marshal(value)
	if err != nil {
		pr, pw := io.Pipe()
		pw.CloseWithError(err)
		return pr
	}
	return bytes.NewReader(data)
}

type FatalError struct {
	error
}

func Fatal(err error) FatalError {
	return FatalError{err}
}

func (FatalError) Is(err error) bool {
	_, ok := err.(FatalError)
	return ok
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
		if errors.Is(err, FatalError{}) || time.Now().After(deadline) {
			require.NoErrorf(t, err, msg, args...)
			return
		}
		ticker.Reset(tick)
	}
}
