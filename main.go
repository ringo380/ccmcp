package main

import (
	"fmt"
	"os"

	"github.com/ringo380/ccmcp/cmd"
)

var version = "0.2.0-dev"

func main() {
	if err := cmd.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
