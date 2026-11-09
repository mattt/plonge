// Command plonge finds the caches that developer tools leave on disk.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/report"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

const usage = `Usage: plonge <command> [options]

Plonge finds the caches that developer tools and coding agents leave on disk.
This version only reports; it deletes nothing.

Commands:
  scan    Report caches in mise projects and other roots
  disk    Print the free space on your home directory's volume

Run "plonge <command> --help" for the options of a command.
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
	case "disk":
		return runDisk(args[1:], stdout, stderr)
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
	results := scan.Run(context.Background(), roots, rs)
	rep := build(results, rs.QuietHours, broken, time.Now())
	if opts.json {
		err = report.WriteJSON(stdout, rep)
	} else {
		for _, e := range rep.Errors {
			fmt.Fprintf(stderr, "plonge scan: %s\n", e)
		}
		err = report.WriteTable(stdout, rep, env.Home)
	}
	if err != nil {
		fmt.Fprintf(stderr, "plonge scan: %v\n", err)
		return 1
	}
	return 0
}

const diskUsage = `Usage: plonge disk [--warn-below <size>]

Disk prints the free space on your home directory's volume.
With --warn-below, it prints one warning line only when free space is below
the size, and always exits 0, so that hooks can call it.

Options:
`

func runDisk(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("disk", flag.ContinueOnError)
	var parseOut strings.Builder
	fs.SetOutput(&parseOut)
	warnBelow := fs.String("warn-below", "", "warn only when free space is below `size`, such as 20GiB")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), diskUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(stdout)
			fs.Usage()
			return 0
		}
		fmt.Fprint(stderr, parseOut.String())
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "plonge disk: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	// A hook must not fail because of plonge, so --warn-below always exits 0.
	failed := 1
	if *warnBelow != "" {
		failed = 0
	}
	var floor int64
	if *warnBelow != "" {
		var err error
		if floor, err = disk.ParseSize(*warnBelow); err != nil {
			fmt.Fprintf(stderr, "plonge disk: %v\n", err)
			return failed
		}
	}
	home := mise.EnvFromOS().Home
	free, err := disk.Free(home)
	if err != nil {
		fmt.Fprintf(stderr, "plonge disk: %v\n", err)
		return failed
	}
	if *warnBelow == "" {
		fmt.Fprintf(stdout, "Free space on ~: %s\n", report.HumanBytes(free))
	} else if free < floor {
		fmt.Fprintf(stdout, "Warning: only %s free on ~, below %s. Run plonge scan to find caches.\n",
			report.HumanBytes(free), report.HumanBytes(floor))
	}
	return 0
}

// build sizes every candidate across all roots at once and makes the report.
func build(results []scan.Root, quietHours float64, broken int, now time.Time) report.Report {
	var dirs []string
	for _, r := range results {
		for _, c := range r.Candidates {
			dirs = append(dirs, c.Path)
		}
	}
	flat := scan.Sizes(dirs)
	sizes := make([][]int64, len(results))
	k := 0
	for i, r := range results {
		sizes[i] = flat[k : k+len(r.Candidates)]
		k += len(r.Candidates)
	}
	return report.Build(results, sizes, quietHours, broken, now)
}
