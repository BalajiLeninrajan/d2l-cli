// Command d2l is a terminal client for D2L Brightspace.
package main

import (
	"os"

	"github.com/BalajiLeninrajan/d2l-cli/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
