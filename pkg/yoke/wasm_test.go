package yoke

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yokecd/yoke/internal/x"
)

func TestEvalFlightWithDeterministicWASI(t *testing.T) {
	flightPath := filepath.Join(t.TempDir(), "flight.wasm")
	require.NoError(t, x.X(
		"go build -o "+flightPath+" ../../cmd/yoke/internal/testing/flights/nondeterministic",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))

	params := EvalParams{
		Release: "reproducible-flight",
		Flight: FlightParams{
			Path:          flightPath,
			Deterministic: true,
		},
	}

	first, err := EvalFlight(context.Background(), params)
	require.NoError(t, err)
	second, err := EvalFlight(context.Background(), params)
	require.NoError(t, err)

	require.JSONEq(t, string(first), string(second))
}
