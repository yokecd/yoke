package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

// verifyFlightReproducibility evaluates a flight twice with the same module,
// input, arguments, environment, and cluster-access configuration. It compares
// parsed Kubernetes objects rather than raw stdout so serialization ordering
// does not produce false positives.
func verifyFlightReproducibility(ctx context.Context, commander yoke.Commander, params *yoke.TakeoffParams) error {
	if err := yoke.LoadWasm(ctx, &params.Flight); err != nil {
		return fmt.Errorf("failed to load wasm for reproducibility check: %w", err)
	}

	input, hadInput, err := snapshotFlightInput(params.Flight.Input)
	if err != nil {
		return fmt.Errorf("failed to snapshot flight input for reproducibility check: %w", err)
	}
	params.Flight.Input = replayFlightInput(input, hadInput)

	first, err := evaluateFlightForReproducibility(ctx, commander, *params, input, hadInput)
	if err != nil {
		return fmt.Errorf("first reproducibility evaluation failed: %w", err)
	}
	second, err := evaluateFlightForReproducibility(ctx, commander, *params, input, hadInput)
	if err != nil {
		return fmt.Errorf("second reproducibility evaluation failed: %w", err)
	}

	return compareDesiredStates(first, second)
}

func evaluateFlightForReproducibility(
	ctx context.Context,
	commander yoke.Commander,
	params yoke.TakeoffParams,
	input []byte,
	hadInput bool,
) ([]byte, error) {
	var stdout bytes.Buffer

	params.SendToStdout = true
	params.DiffOnly = false
	params.Out = ""
	params.Flight.Input = replayFlightInput(input, hadInput)
	// Let the WASI runtime buffer stderr so execution errors retain their flight output.
	params.Flight.Stderr = nil

	probeCtx := internal.WithStdout(ctx, &stdout)
	if err := commander.Takeoff(probeCtx, params); err != nil {
		return nil, err
	}

	return stdout.Bytes(), nil
}

func snapshotFlightInput(input io.Reader) ([]byte, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	data, err := io.ReadAll(input)
	return data, true, err
}

func replayFlightInput(input []byte, hadInput bool) io.Reader {
	if !hadInput {
		return nil
	}
	return bytes.NewReader(input)
}

func compareDesiredStates(first, second []byte) error {
	firstStages, err := internal.ParseStages(first)
	if err != nil {
		return fmt.Errorf("failed to parse first reproducibility evaluation: %w", err)
	}
	secondStages, err := internal.ParseStages(second)
	if err != nil {
		return fmt.Errorf("failed to parse second reproducibility evaluation: %w", err)
	}

	if len(firstStages) != len(secondStages) {
		return fmt.Errorf(
			"flight output is not reproducible: stage count changed between equivalent evaluations (%d -> %d)",
			len(firstStages),
			len(secondStages),
		)
	}

	for i := range firstStages {
		firstResources := internal.CanonicalObjectMap(firstStages[i])
		secondResources := internal.CanonicalObjectMap(secondStages[i])
		if reflect.DeepEqual(firstResources, secondResources) {
			continue
		}

		changed := changedResources(firstResources, secondResources)
		if len(changed) == 0 {
			return fmt.Errorf("flight output is not reproducible: stage %d changed between equivalent evaluations", i+1)
		}
		return fmt.Errorf(
			"flight output is not reproducible: stage %d changed between equivalent evaluations (resources: %v)",
			i+1,
			changed,
		)
	}

	return nil
}

func changedResources(first, second map[string]any) []string {
	keys := make(map[string]struct{}, len(first)+len(second))
	for key := range first {
		keys[key] = struct{}{}
	}
	for key := range second {
		keys[key] = struct{}{}
	}

	changed := make([]string, 0, len(keys))
	for key := range keys {
		if !reflect.DeepEqual(first[key], second[key]) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}
