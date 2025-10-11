package main

import (
	"flag"
	"fmt"
)

func main() {
	count := flag.Int("mb", 1, "how much memory to allocate")
	flag.Parse()

	data := make([]byte, (*count)*1<<20)
	for i := range data {
		data[i] = byte(i)
	}

	fmt.Println("[]")
}
