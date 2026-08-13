package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/command"
)

// Version is the application version, injected at build time via ldflags
var Version = "dev"

func main() {
	// Set the build version from the build info if not set by the build system
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				Version = bi.Main.Version
			}
		}
	}

	if err := command.Execute(Version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
