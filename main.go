package main

import (
	"fmt"
	"os"

	"github.com/ringo380/ccmcp/cmd"
)

// These are stamped at build time by goreleaser (see .goreleaser.yaml) via
//
//	-ldflags "-X main.version=... -X main.commit=... -X main.date=..."
//
// `go build` without ldflags leaves the defaults, which is fine for local development.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := cmd.Execute(fullVersion()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func fullVersion() string {
	if version == "dev" {
		return version
	}
	return fmt.Sprintf("%s (commit %s, built %s)", version, shortCommit(commit), date)
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
