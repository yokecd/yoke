package main

import (
	"os"
	"slices"
	"testing"
)

func TestFlagCompletionsDescent(t *testing.T) {
	comps :=
		getFlagCompletion(
			[]string{"yoke", "descent", "-"},
			validCommands["descent"],
		)
	if !slices.Equal(
		comps, []string{
			"-debug",
			"-kube-context",
			"-namespace",
			"-poll",
			"-remove-all",
			"-remove-crds",
			"-wait",
			"-kubeconfig",
			"-lock",
			"-remove-namespaces",
		}) {
		t.Fatal("TestDescentFlagCompletions did not yield expected flags, got: ", comps, "ARGS: ", os.Args)
	}
}
