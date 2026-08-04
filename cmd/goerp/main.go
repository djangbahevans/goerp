package main

import (
	"os"

	"github.com/djangbahevans/goerp/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
