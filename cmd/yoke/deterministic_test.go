package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetTakeoffParamsEnablesDeterministicFlight(t *testing.T) {
	testSettings := settings
	testSettings.Debug = new(bool)

	params, err := GetTakeoffParams(testSettings, nil, []string{
		"-deterministic",
		"reproducible-flight",
		"flight.wasm",
	})

	require.NoError(t, err)
	require.True(t, params.Flight.Deterministic)
}
