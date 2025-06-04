package main

import (
	"fmt"
	"os"

	"github.com/yokecd/yoke/pkg/flight"
	"golang.org/x/mod/semver"
)

// let's hope we never reach this version otherwise we break out tests.
var minVersion = "v999.999.999"

func main() {
	if semver.Compare(flight.YokeVersion(), minVersion) < 0 {
		fmt.Fprintln(os.Stderr, "failed to meet min version requirement for yoke")
		os.Exit(1)
	}
}
