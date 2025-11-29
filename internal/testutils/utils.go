package testutils

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
