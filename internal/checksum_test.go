package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChecksumFromPath(t *testing.T) {
	checksum := "sumchecksum"

	for _, path := range []string{
		"oci://registry/module:sha256_" + checksum,
		"file://path/to/module_sha256_" + checksum,
		"file://path/to/sha256_" + checksum,
		"file://path/to/sha256_" + checksum + ".wasm",
		"file://path/to/sha256_" + checksum + ".wasm.gz",
		"./path/to/sha256_" + checksum,
		"https://domain.com/some/module_sha256_" + checksum,
		"https://domain.com/some/sha256_" + checksum,
		"https://domain.com/some/sha256_" + checksum + ".wasm.gz",
	} {
		require.Equal(t, checksum, ChecksumFromPath(path))
	}

	for _, path := range []string{
		"oci://registry/module:v1.2.3",
		"https://domain.com/sha256/checksum",
		"./local/fs/sha1_sha1",
		"./local/fs/sha1_sha1.wasm",
		"./local/fs/sha1_sha1.wasm.gz",
	} {
		require.Empty(t, ChecksumFromPath(path))
	}
}
