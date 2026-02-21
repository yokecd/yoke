package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPinnableReference(t *testing.T) {
	for _, ref := range []string{
		"oci://ghcr.io/repo/mod:v1.2.3",
		"oci://ghcr.io/repo/mod:1.2.3",
		"oci://ghcr.io/repo/mod:v1",
		"oci://ghcr.io/repo/mod:1",
		"oci://ghcr.io/repo/mod:v1.2.3-prerelease",
		"oci://ghcr.io/repo/mod:1.2.3-prerelease",
		"oci://ghcr.io/repo/mod:v1.2.3-prerelease+build",
		"oci://ghcr.io/repo/mod:1.2.3-prerelease+build",
		"http://domain.io/module.wasm.gz",
	} {
		require.Truef(t, IsPinnableReference(ref), "expected %s to be pinnable but is not", ref)
	}

	for _, ref := range []string{
		"oci://ghcr.io/repo/mod",
		"oci://ghcr.io/repo/mod:latest",
		"oci://ghcr.io/repo/mod:stable",
		"oci://ghcr.io/repo/mod:experimental",
		"file://./main.wasm",
	} {
		require.Falsef(t, IsPinnableReference(ref), "expected %s to not be pinnable but is", ref)
	}
}
