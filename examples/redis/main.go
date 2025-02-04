package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/yokecd/yoke/examples/internal/flights/redis"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	resources, err := redis.RenderChart(os.Args[0], os.Getenv("NAMESPACE"), &redis.Values{})
	if err != nil {
		return fmt.Errorf("failed to render chart resources: %w", err)
	}

	return json.NewEncoder(os.Stdout).Encode(resources)
}
