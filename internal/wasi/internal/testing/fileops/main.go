package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	write := flag.Bool("write", false, "write data to the file")
	flag.Parse()

	file, err := os.OpenFile("./test.txt", os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(os.Stdout, file); err != nil {
		return err
	}

	if !*write {
		return nil
	}

	_, err = io.WriteString(file, "this should fail")
	return err
}
