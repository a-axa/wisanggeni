// Command wisanggeni hides files and folders inside PNG images.
//
// The binary is a thin entry point: every subcommand lives in cmd/, and the
// engine it drives lives in internal/. Running it without a subcommand opens
// the TUI.
package main

import (
	"os"

	"github.com/a-axa/wisanggeni/cmd"
)

// version is replaced at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cmd.Execute(version))
}
