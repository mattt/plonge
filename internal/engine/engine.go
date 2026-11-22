// Package engine runs plonge's operations for the command line and the MCP server.
package engine

import (
	"context"
	"time"

	"github.com/mattt/plonge/internal/agents"
	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/procs"
	"github.com/mattt/plonge/internal/report"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// Selection names the project roots to scan.
type Selection struct {
	Roots  []string // extra roots, as absolute paths
	NoMise bool     // scan only Roots
	Global bool     // also list the global caches
}

// Inventory is what one scan found.
type Inventory struct {
	Env    mise.Env
	Rules  rules.Rules
	Roots  []scan.Root
	Sizes  [][]int64
	Report report.Report
	Now    time.Time
	// Sources reports each source of activity: agents and running processes.
	Sources []scan.Source
}

// LoadRules reads the default and user rules.
func LoadRules(env mise.Env) (rules.Rules, error) {
	return rules.Load(rules.UserPath(env.XDGConfigHome, env.Home))
}

// Inspect finds and measures the caches in the selected roots.
func Inspect(ctx context.Context, sel Selection) (Inventory, error) {
	env := mise.EnvFromOS()
	inv := Inventory{Env: env, Now: time.Now()}
	var err error
	if inv.Rules, err = LoadRules(env); err != nil {
		return inv, err
	}
	var paths []string
	broken := 0
	if !sel.NoMise {
		p, err := mise.ReadProjects(env)
		if err != nil {
			return inv, err
		}
		paths = append(paths, p.Roots...)
		broken = p.BrokenLinks
	}
	paths = append(paths, sel.Roots...)
	// Only a root in a Git work tree covers the roots inside it.
	// Detect skips a root outside one, so the roots inside it stay.
	inTree := scan.WorkTrees(ctx, paths)
	paths = mise.DropNested(paths, func(root string) bool { return inTree[root] })
	inv.Roots = scan.Run(ctx, paths, inv.Rules)
	inv.Sources = agents.Apply(ctx, inv.Roots, env.Home, inv.Now)
	inv.Sources = append(inv.Sources, scan.RefuseUsed(ctx, inv.Roots, procs.System()))
	inv.Sizes = scan.Measure(ctx, inv.Roots)
	inv.Report = report.Build(inv.Roots, inv.Sizes, inv.Rules.QuietHours, broken, inv.Now)
	inv.Report.ActivitySources = inv.Sources
	if sel.Global {
		inv.Report.Global = global.New(env.Home, inv.Rules.TmpDirAgeHours).List(ctx, inv.Rules.Global)
	}
	return inv, nil
}

// CleanOptions choose what Clean deletes.
type CleanOptions struct {
	Selection
	Apply         bool
	IncludeActive bool
	MinSize       int64
	// Confirm, when set with Apply, receives the dry-run result
	// of the same scan before anything is deleted.
	// An error from it stops the clean before any deletion.
	Confirm func(dry clean.Result) error
}

// Clean scans the selected roots and cleans the caches that qualify.
func Clean(ctx context.Context, opts CleanOptions) (clean.Result, Inventory, error) {
	inv, err := Inspect(ctx, opts.Selection)
	if err != nil {
		return clean.Result{}, inv, err
	}
	copts := clean.Options{
		IncludeActive: opts.IncludeActive,
		MinSize:       opts.MinSize,
		QuietHours:    inv.Rules.QuietHours,
		Now:           inv.Now,
	}
	if opts.Apply && opts.Confirm != nil {
		if err := opts.Confirm(clean.Run(ctx, inv.Roots, inv.Sizes, copts)); err != nil {
			return clean.Result{}, inv, err
		}
	}
	copts.Apply = opts.Apply
	copts.StillBusy = func(root string) string { return agents.Busy(ctx, root, inv.Env.Home) }
	copts.InUse = clean.InUseChecker(ctx, procs.System(), time.Now)
	var res clean.Result
	run := func() { res = clean.Run(ctx, inv.Roots, inv.Sizes, copts) }
	if opts.Apply {
		change := disk.Track(inv.Env.Home, run)
		res.HomeVolume = &change
	} else {
		run()
	}
	res.ActivitySources = inv.Sources
	return res, inv, nil
}

// GlobalOptions choose what CleanGlobal runs.
type GlobalOptions struct {
	global.Options
	// Confirm, when set with Apply, receives the dry-run result
	// of the same listing before any action runs.
	// An error from it stops the clean before any action.
	Confirm func(dry global.Result) error
}

// CleanGlobal runs the global rules' clean commands, or lists them without Apply.
func CleanGlobal(ctx context.Context, opts GlobalOptions) (global.Result, mise.Env, error) {
	env := mise.EnvFromOS()
	rs, err := LoadRules(env)
	if err != nil {
		return global.Result{}, env, err
	}
	r := global.New(env.Home, rs.TmpDirAgeHours)
	entries := r.List(ctx, rs.Global)
	dry := global.Options{IncludeClear: opts.IncludeClear}
	if !opts.Apply {
		return r.Clean(ctx, entries, dry), env, nil
	}
	if opts.Confirm != nil {
		if err := opts.Confirm(r.Clean(ctx, entries, dry)); err != nil {
			return global.Result{}, env, err
		}
	}
	// The listing runs only lookups and checks, so measure after it.
	var res global.Result
	change := disk.Track(env.Home, func() { res = r.Clean(ctx, entries, opts.Options) })
	res.HomeVolume = &change
	return res, env, nil
}
