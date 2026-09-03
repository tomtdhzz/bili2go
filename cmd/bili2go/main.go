package main

import (
	"os"

	"bili2go/internal/delivery/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
