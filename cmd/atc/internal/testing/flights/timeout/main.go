package main

import "time"

func main() {
	for range time.NewTicker(10 * time.Millisecond).C {
	}
}
