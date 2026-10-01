// Command alror is the Alror CLI.
package main

import (
	"os"

	"github.com/manaskumar3003/alror-cli/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
