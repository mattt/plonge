// Command plonge finds the caches that developer tools leave on disk.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/rules"
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

const scanUsage = `Usage: plonge scan [--root <dir>]... [--no-mise] [--json]

Scan reports the caches in each project that mise tracks and in each --root.
It only reads; it deletes nothing.

Options:
`

// rootList collects repeated --root flags.
type rootList []string

func (r *rootList) String() string { return strings.Join(*r, ",") }

func (r *rootList) Set(v string) error {
	abs, err := filepath.Abs(v)
	if err != nil {
		return err
	}
	*r = append(*r, abs)
	return nil
}

type scanOptions struct {
	roots  rootList
	noMise bool
	json   bool
}

func parseScan(args []string, stdout, stderr io.Writer) (scanOptions, int, bool) {
	var opts scanOptions
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	var parseOut strings.Builder
	fs.SetOutput(&parseOut)
	fs.Var(&opts.roots, "root", "add a project `dir` to scan (repeatable)")
	fs.BoolVar(&opts.noMise, "no-mise", false, "scan only the --root directories")
	fs.BoolVar(&opts.json, "json", false, "print the report as JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), scanUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(stdout)
			fs.Usage()
			return opts, 0, false
		}
		fmt.Fprint(stderr, parseOut.String())
		return opts, 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "plonge scan: unexpected argument %q\n", fs.Arg(0))
		return opts, 2, false
	}
	if opts.noMise && len(opts.roots) == 0 {
		fmt.Fprintln(stderr, "plonge scan: --no-mise needs at least one --root")
		return opts, 2, false
	}
	return opts, 0, true
}

// collectRoots combines mise's projects with the --root directories.
func collectRoots(opts scanOptions, env mise.Env) ([]string, int, error) {
	var roots []string
	broken := 0
	if !opts.noMise {
		p, err := mise.ReadProjects(env)
		if err != nil {
			return nil, 0, err
		}
		roots = append(roots, p.Roots...)
		broken = p.BrokenLinks
	}
	roots = append(roots, opts.roots...)
	return mise.DropNested(roots), broken, nil
}

func runScan(args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseScan(args, stdout, stderr)
	if !ok {
		return code
	}
	env := mise.EnvFromOS()
	rs, err := rules.Load(rules.UserPath(env.XDGConfigHome, env.Home))
	if err != nil {
		fmt.Fprintf(stderr, "plonge scan: %v\n", err)
		return 1
	}
	roots, broken, err := collectRoots(opts, env)
	if err != nil {
		fmt.Fprintf(stderr, "plonge scan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%d roots, %d broken links, %d rule names\n", len(roots), broken, len(rs.Names))
	return 0
}
