package main

import (
	"slices"
	"testing"
)

func TestFlagCompletionsDescent(t *testing.T) {
	if !slices.Equal(
		getFlagCompletion(
			[]string{"yoke", "descent"},
			validCommands["descent"],
		), []string{
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
		t.Fatalf("TestDescentFlagCompletions did not yield expected flags")
	}
}
