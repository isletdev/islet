// Command islet is the command line. The released binary is isletd, which
// dispatches into the same code when it is invoked through the islet symlink;
// this entrypoint exists so the CLI can be built and run on its own during
// development.
package main

import (
	"os"

	"github.com/isletdev/islet/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args)) }
