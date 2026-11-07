// Command plonge finds the caches that developer tools leave on disk.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `Usage: plonge <command> [options]

Plonge finds the caches that developer tools and coding agents leave on disk.
This version only reports; it deletes nothing.

Commands:
  scan    Report caches in mise projects and other roots

Run "plonge scan --help" for the options of scan.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "scan":
		return runScan(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "plonge: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "plonge scan: not implemented yet")
	return 0
}
