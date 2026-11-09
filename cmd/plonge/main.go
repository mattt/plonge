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

	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/engine"
	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/report"
)

const usage = `Usage: plonge <command> [options]

Plonge finds the caches that developer tools and coding agents leave on disk.

Commands:
  scan    Report caches in mise projects and other roots
  clean   Delete caches in quiet projects (a dry run unless --apply)
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
	case "clean":
		return runClean(args[1:], stdout, stderr)
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

func (o *scanOptions) bind(fs *flag.FlagSet) {
	fs.Var(&o.roots, "root", "add a project `dir` to scan (repeatable)")
	fs.BoolVar(&o.noMise, "no-mise", false, "scan only the --root directories")
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
}

func (o *scanOptions) selection() engine.Selection {
	return engine.Selection{Roots: o.roots, NoMise: o.noMise}
}

// parse parses args with fs and reports whether the command should go on.
// When it should not, code is the exit code.
func parse(fs *flag.FlagSet, usage string, args []string, stdout, stderr io.Writer) (code int, ok bool) {
	var parseOut strings.Builder
	fs.SetOutput(&parseOut)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(stdout)
			fs.Usage()
			return 0, false
		}
		fmt.Fprint(stderr, parseOut.String())
		return 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "plonge %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func checkRoots(name string, o scanOptions, stderr io.Writer) bool {
	if o.noMise && len(o.roots) == 0 {
		fmt.Fprintf(stderr, "plonge %s: --no-mise needs at least one --root\n", name)
		return false
	}
	return true
}

func runScan(args []string, stdout, stderr io.Writer) int {
	var opts scanOptions
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	opts.bind(fs)
	if code, ok := parse(fs, scanUsage, args, stdout, stderr); !ok {
		return code
	}
	if !checkRoots("scan", opts, stderr) {
		return 2
	}
	inv, err := engine.Inspect(context.Background(), opts.selection())
	if err != nil {
		fmt.Fprintf(stderr, "plonge scan: %v\n", err)
		return 1
	}
	rep := inv.Report
	if opts.json {
		err = report.WriteJSON(stdout, rep)
	} else {
		for _, e := range rep.Errors {
			fmt.Fprintf(stderr, "plonge scan: %s\n", e)
		}
		err = report.WriteTable(stdout, rep, inv.Env.Home)
	}
	if err != nil {
		fmt.Fprintf(stderr, "plonge scan: %v\n", err)
		return 1
	}
	return 0
}

const cleanUsage = `Usage: plonge clean [--apply] [--include-active] [--min-size <size>]
                    [--root <dir>]... [--no-mise] [--json]

Clean selects caches as scan does and prints what it would delete.
With --apply, it deletes them. Only caches in quiet projects qualify,
unless you give --include-active.

Options:
`

func runClean(args []string, stdout, stderr io.Writer) int {
	var opts scanOptions
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	opts.bind(fs)
	apply := fs.Bool("apply", false, "delete the caches; without it, only print them")
	includeActive := fs.Bool("include-active", false, "also delete caches in projects that are not quiet")
	minSize := fs.String("min-size", "", "skip caches smaller than `size`, such as 100MiB")
	if code, ok := parse(fs, cleanUsage, args, stdout, stderr); !ok {
		return code
	}
	if !checkRoots("clean", opts, stderr) {
		return 2
	}
	var floor int64
	if *minSize != "" {
		var err error
		if floor, err = disk.ParseSize(*minSize); err != nil {
			fmt.Fprintf(stderr, "plonge clean: --min-size: %v\n", err)
			return 2
		}
	}
	res, inv, err := engine.Clean(context.Background(), engine.CleanOptions{
		Selection:     opts.selection(),
		Apply:         *apply,
		IncludeActive: *includeActive,
		MinSize:       floor,
	})
	if err != nil {
		fmt.Fprintf(stderr, "plonge clean: %v\n", err)
		return 1
	}
	if opts.json {
		err = report.WriteJSON(stdout, res)
	} else {
		for _, e := range res.Errors {
			fmt.Fprintf(stderr, "plonge clean: %s\n", e)
		}
		err = report.WriteClean(stdout, res, inv.Env.Home)
	}
	if err != nil {
		fmt.Fprintf(stderr, "plonge clean: %v\n", err)
		return 1
	}
	if res.Failed > 0 || len(res.Errors) > 0 {
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
	warnBelow := fs.String("warn-below", "", "warn only when free space is below `size`, such as 20GiB")
	if code, ok := parse(fs, diskUsage, args, stdout, stderr); !ok {
		return code
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
