package main

import (
	"reflect"
	"testing"
)

// TestCmdSubs asserts that all of the expected sub commands are in place
func TestCmdSubs(t *testing.T) {
	// this is dumb
	validCommands := []string{
		"atc",
		"mayday",
		"unlatch",
		"schematics",
		"verify",
		"version",
		"takeoff",
		"descent",
		"blackbox",
		"turbulence",
		"stow",
		"sign",
		"inspect",
		"push",
		"drift",
		"diff",
		"delete",
		"meta",
		"apply",
		"up",
		"down",
		"restore",
		"unlock",
	}
	for _, cmd := range validCommands {
		t.Logf("Checking SubCommand for root: %v", cmd)
		if _, ok := CmdRoot.SubCommands[cmd]; !ok {
			t.Fatalf("expected %s to be a subcommand of the root command", cmd)
		}
	}
	for _, cmd := range []string{"ls", "set", "get"} {
		t.Logf("Checking SubCommand for schematics %v", cmd)
		if _, ok := CmdSchematics.SubCommands[cmd]; !ok {
			t.Fatalf("expected %s to be a subcommand of the schematics command", cmd)
		}
	}
}

// TestCmdSeekRunner tests that the Seek function can find the correct
// sub command runner given a slice of args
func TestCmdSeekRunner(t *testing.T) {
	type tMatrix struct {
		Want CmdRunner
		Args []string
	}
	tests := []tMatrix{
		{Want: CmdRoot.Runner, Args: []string{"yoke"}},
		{Want: CmdSchematicsGet.Runner, Args: []string{"yoke", "schematics", "get"}},
		{Want: CmdSchematicsLs.Runner, Args: []string{"yoke", "schematics", "ls"}},
		{Want: CmdSchematicsSet.Runner, Args: []string{"yoke", "schematics", "set"}},
	}
	for k, c := range CmdRoot.SubCommands {
		tests = append(tests, tMatrix{
			Want: c.Runner,
			Args: []string{"yoke", k},
		})
	}
	for _, s := range tests {
		runner, _ := Seek(s.Args)
		t.Logf("Checking runner for %v", s.Args)
		if reflect.ValueOf(runner) != reflect.ValueOf(s.Want) {
			t.Fatalf("got function mismatch for %v", s.Args)
		}
	}
}
