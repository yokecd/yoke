package main

import (
	"encoding/json"
	"os"

	"github.com/yokecd/yoke/cmd/examples/internal/flights/mongodb"
)

func main() {
	resources, err := mongodb.RenderChart(os.Args[0], "default", &mongodb.Values{
		// ... values ...
	})
	if err != nil {
		panic(err)
	}
	json.NewEncoder(os.Stdout).Encode(resources)
}
