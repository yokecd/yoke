package internal

import (
	"net/url"
	"strings"

	"golang.org/x/mod/semver"
)

func IsPinnableReference(ref string) bool {
	uri, err := url.Parse(ref)
	if err != nil {
		return false
	}
	switch uri.Scheme {
	// local wasm files are for development.
	// We can assume the user is deliberately developing modules or at the very least
	// that they are responsible for managing and deploying from modules on their own system.
	case "file":
		return false

	// If the tag of an oci module is semver we pin that version.
	// Otherwise it could be a semantically meaningful tag such as "latest" or "nightly",
	// which is inherently expected to change over time.
	case "oci":
		_, tag, _ := strings.Cut(uri.Path, ":")
		if tag == "" {
			return false // same as latest
		}
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		return semver.IsValid(tag)

	// For modules hosted over http, there is no standard. Hence we can't use any existing convention.
	// We may in the future decide to define a convention such as semver in the last path segment.
	// But for now we treat modules over http as "dangerous/volatile" and we pin them.
	case "http", "https":
		return true

	// No other known cases for now. We could panic, but let's just pin as a safe default.
	default:
		return true
	}
}
