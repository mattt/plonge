package main

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"
)

// version is set at link time in release builds.
var version string

const versionUsage = `Usage: plonge version

Version prints the version of Plonge.
`

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if code, ok := parse(fs, versionUsage, args, stdout, stderr); !ok {
		return code
	}
	fmt.Fprintln(stdout, resolveVersion(version, debug.ReadBuildInfo))
	return 0
}

// resolveVersion returns the injected version.
// Without one, as with go install, it returns the module version,
// or "dev" if the build has no module version.
func resolveVersion(injected string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if injected != "" {
		return injected
	}
	info, ok := buildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
