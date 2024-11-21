package testutils

import (
	"bytes"
	"encoding/json"
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
