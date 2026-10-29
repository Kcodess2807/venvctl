package main

import (
	"os"

	"pygo/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
