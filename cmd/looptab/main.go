package main

import (
	"fmt"
	"os"

	"github.com/ryangerardwilson/looptab/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(os.Args[1:], version); err != nil {
		fmt.Fprintf(os.Stderr, "looptab: %v\n", err)
		os.Exit(1)
	}
}
