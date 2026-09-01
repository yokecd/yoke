package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yokecd/yoke/internal/x"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestCompareDesiredStatesIgnoresSerializationOrder(t *testing.T) {
	first := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"example"},"data":{"a":"1","b":"2"}}`)
	second := []byte(`{"data":{"b":"2","a":"1"},"metadata":{"name":"example"},"kind":"ConfigMap","apiVersion":"v1"}`)

	require.NoError(t, compareDesiredStates(first, second))
}

func TestVerifyReproducibleRejectsNondeterministicFlight(t *testing.T) {
	outputDir := filepath.Join("test_output", "reproducibility")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(outputDir) })

	flightPath := filepath.Join(outputDir, "flight.wasm")
	require.NoError(t, x.X(
		"go build -o "+flightPath+" ./internal/testing/flights/nondeterministic",
		x.Env("GOOS=wasip1", "GOARCH=wasm"),
	))

	err := TakeOff(background, TakeoffParams{
		GlobalSettings:      settings,
		VerifyReproducible: true,
		TakeoffParams: yoke.TakeoffParams{
			Release: "non-reproducible-flight",
			Flight: yoke.FlightParams{
				Path: flightPath,
			},
		},
	})

	require.ErrorContains(t, err, "reproducibility check failed")
	require.ErrorContains(t, err, "flight output is not reproducible")
	require.ErrorContains(t, err, "ConfigMap")
}
