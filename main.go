package main

import (
	"os"

	"github.com/eurafaeldecarvalho/tf-exclude/internal/cli"
)

// version is set by goreleaser via -X main.version.
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], version))
}
