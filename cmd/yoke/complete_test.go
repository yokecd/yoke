package main

import (
	"slices"
	"testing"
)

// returns true if a is a subset of b
func isSubset(a, b []string) bool {
	set := make(map[string]bool)
	for _, s := range b {
		set[s] = true
	}
	for _, s := range a {
		_, ok := set[s]
		if !ok {
			return false
		}
	}
	return true
}

func TestCompFlags(t *testing.T) {
	t.Setenv("COMP_LINE", "")
	for _, comp := range []struct {
		Command *YokeCommand
		Args    []string
		Wanted  []string
	}{
		{
			Command: CmdATC,
			Args:    []string{"-"},
			Wanted: []string{
				"-debug-file",
			},
		},
		{
			Command: CmdBlackbox,
			Args:    []string{"-"},
			Wanted: []string{
				"-context",
				"-namespace",
			},
		},
		{
			Command: CmdDescent,
			Args:    []string{"-"},
			Wanted: []string{
				"-lock",
				"-namespace",
				"-poll",
				"-remove-all",
				"-remove-crds",
				"-remove-namespaces",
				"-wait",
			},
		},
		{
			Command: CmdMayday,
			Args:    []string{"-"},
			Wanted: []string{
				"-namespace",
				"-remove-all",
				"-remove-crds",
				"-remove-namespaces",
			},
		},
		{
			Command: CmdSchematics,
			Args:    []string{"-"},
			Wanted:  []string{"-wasm"},
		},
		{
			Command: CmdSign,
			Args:    []string{"-"},
			Wanted: []string{
				"-key",
				"-o",
				"-f",
			},
		},
		{
			Command: CmdStow,
			Args:    []string{"-"},
			Wanted: []string{
				"-insecure",
				"-tag",
			},
		},
		{
			Command: CmdTakeoff,
			Args:    []string{"-"},
			Wanted: []string{
				"-checksum",
				"-color",
				"-context",
				"-cross-namespace",
				"-diff-only",
				"-dry",
				"-insecure",
				"-remove-crds",
				"-cluster-access",
				"-out",
				"-remove-namespaces",
				"-timeout",
				"-verify",
				"-wait",
				"-create-namespace",
				"-force-conflicts",
				"-history-cap",
				"-poll",
				"-resource-access",
				"-stdout",
				"-compilation-cache",
				"-force-ownership",
				"-lock",
				"-max-memory-mib",
				"-namespace",
				"-remove-all",
				"-skip-dry-run",
			},
		},
		{
			Command: CmdTurbulence,
			Args:    []string{"-"},
			Wanted: []string{
				"-conflict-only",
				"-context",
				"-fix",
				"-namespace",
				"-color",
			},
		},
		{
			Command: CmdUnlatch,
			Args:    []string{"-"},
			Wanted:  []string{"-namespace"},
		},
		{
			Command: CmdVerify,
			Args:    []string{"-"},
			Wanted:  []string{"-key"},
		},
	} {
		compLine := getFlagCompletion(comp.Args, comp.Command)
		// We can't do a full compare here because 'go test' introduces its own flag sets
		if !isSubset(comp.Wanted, compLine) {
			t.Fatalf("%s did not yield expected flags, got: %s args: %s", comp.Command.Name, compLine, comp.Args)
		}
	}
}

func TestCompFlagDuplicates(t *testing.T) {
	t.Setenv("COMP_LINE", "")
	for _, comp := range []struct {
		Comp      []string
		Command   *YokeCommand
		Args      []string
		NotInComp []string
	}{
		{
			Args:      []string{"-context", "-"},
			Command:   CmdBlackbox,
			NotInComp: []string{"-context"},
		},
		{
			Args:      []string{"-context", "-namespace"},
			Command:   CmdBlackbox,
			NotInComp: []string{"-context", "-namespace"},
		},
	} {
		compLine := getFlagCompletion(comp.Args, comp.Command)
		for _, unwanted := range comp.NotInComp {
			if slices.Contains(compLine, unwanted) {
				t.Fatalf("got unexpected flag completion for %s with args %v: got %v", comp.Command.Name, comp.Args, compLine)
			}
		}
	}
}
