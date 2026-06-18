package ctrl

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminalErrorMatchesInterfaces(t *testing.T) {
	testErr := errors.New("test")
	terminal := Terminal(testErr)
	wrapped := fmt.Errorf("wrapped: %w", terminal)

	require.True(t, errors.Is(wrapped, terminalError{}))
	require.EqualValues(t, testErr, terminal.(interface{ Unwrap() error }).Unwrap())
}
