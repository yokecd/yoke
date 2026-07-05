package main

import (
	"reflect"
	"testing"
)

// TestCmdSubs asserts that all of the expected sub commands are in place
func TestCmdSubs(t *testing.T) {
	validCommands := []string{
		"apply", "atc", "blackbox",
		"delete", "descent", "diff",
		"down", "drift", "inspect",
		"mayday", "meta", "push",
		"restore", "schematics", "sign",
		"stow", "takeoff", "turbulence",
		"unlatch", "unlock", "up",
		"verify", "version",
	}
	for _, cmd := range validCommands {
		if _, ok := CmdRoot.SubCommands[cmd]; !ok {
			t.Fatalf("expected %s to be a subcommand of the root command", cmd)
		}
	}
	for _, cmd := range []string{"ls", "set", "get"} {
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
		cmd, _ := Seek(s.Args)
		if reflect.ValueOf(cmd.Runner) != reflect.ValueOf(s.Want) {
			t.Fatalf("got function mismatch for %v", s.Args)
		}
	}
}

func TestCmdSeekExtraArgs(t *testing.T) {
	type tMatrix struct {
		Want CmdRunner
		Args []string
	}
	tests := []tMatrix{
		{Want: CmdRoot.Runner, Args: []string{"yoke", "--debug", "foo"}},
		{Want: CmdSchematicsGet.Runner, Args: []string{"yoke", "schematics", "get", "--debug", "foo"}},
		{Want: CmdSchematicsLs.Runner, Args: []string{"yoke", "schematics", "ls", "--debug", "foo"}},
		{Want: CmdSchematicsSet.Runner, Args: []string{"yoke", "schematics", "set", "--debug", "foo"}},
	}
	for k, c := range CmdRoot.SubCommands {
		tests = append(tests, tMatrix{
			Want: c.Runner,
			Args: []string{"yoke", k, "--debug", "foo"},
		})
	}
	for _, s := range tests {
		_, args := Seek(s.Args)
		if !isSubset(args, []string{"--debug", "foo"}) || !(len(args) == 2) {
			t.Fatalf("got args mismatch for %v, got: %v", s.Args, args)
		}
	}
	if _, args := Seek([]string{"yoke"}); len(args) != 0 {
		t.Fatalf("expected root command to have zero args after it")
	}
}
