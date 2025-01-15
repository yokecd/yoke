package flight

import (
	"cmp"
	"os"
	"path/filepath"
)

// Release is convenience for fetching the release name within the context of an executing flight.
// This will generally be the name of release passed to "yoke takeoff"
func Release() string {
	if _, release := filepath.Split(os.Getenv("YOKE_RELEASE")); release != "" {
		return release
	}
	_, release := filepath.Split(os.Args[0])
	return release
}

// Namespace is a convenience function for fetching the namespace within the context of an executing flight.
// This will generally be the -namespace flag passed to "yoke takeoff"
func Namespace() string {
	return cmp.Or(os.Getenv("YOKE_NAMESPACE"), os.Getenv("NAMESPACE"))
}

// Status is a basic status representation used for Flights by the ATC as well as for Airways.
type Status struct {
	// Status represents the state of the resource. Typical values are: InProgress, Ready, Error, Terminating.
	Status string `json:"status,omitempty"`
	// Msg is a human readable message describing the status or error if any.
	Msg string `json:"msg,omitempty"`
}
