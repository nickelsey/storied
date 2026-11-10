// Command storied turns a PR's story into an ordered, anchored path through its diff.
package main

import (
	"os"

	"storied/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
