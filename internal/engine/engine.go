// Package engine runs plonge's operations for the command line and the MCP server.
package engine

import (
	"context"
	"time"

	"github.com/mattt/plonge/internal/agents"
	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/mise"
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
	// Sources reports each source of agent activity.
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
	paths = mise.DropNested(append(paths, sel.Roots...))
	inv.Roots = scan.Run(ctx, paths, inv.Rules)
	inv.Sources = agents.Apply(ctx, inv.Roots, env.Home, inv.Now)
	inv.Sizes = measure(inv.Roots)
	inv.Report = report.Build(inv.Roots, inv.Sizes, inv.Rules.QuietHours, broken, inv.Now)
	inv.Report.ActivitySources = inv.Sources
	if sel.Global {
		inv.Report.Global = global.New(env.Home).List(ctx, inv.Rules.Global)
	}
	return inv, nil
}

// measure sizes every candidate across all roots at once.
func measure(results []scan.Root) [][]int64 {
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
	return sizes
}

// CleanOptions choose what Clean deletes.
type CleanOptions struct {
	Selection
	Apply         bool
	IncludeActive bool
	MinSize       int64
}

// Clean scans the selected roots and cleans the caches that qualify.
func Clean(ctx context.Context, opts CleanOptions) (clean.Result, Inventory, error) {
	inv, err := Inspect(ctx, opts.Selection)
	if err != nil {
		return clean.Result{}, inv, err
	}
	res := clean.Run(ctx, inv.Roots, inv.Sizes, clean.Options{
		Apply:         opts.Apply,
		IncludeActive: opts.IncludeActive,
		MinSize:       opts.MinSize,
		QuietHours:    inv.Rules.QuietHours,
		Now:           inv.Now,
		StillBusy:     func(root string) bool { return agents.Busy(ctx, root) },
	})
	res.ActivitySources = inv.Sources
	return res, inv, nil
}

// CleanGlobal runs the global rules' clean commands, or lists them without Apply.
func CleanGlobal(ctx context.Context, opts global.Options) (global.Result, mise.Env, error) {
	env := mise.EnvFromOS()
	rs, err := LoadRules(env)
	if err != nil {
		return global.Result{}, env, err
	}
	r := global.New(env.Home)
	return r.Clean(ctx, r.List(ctx, rs.Global), opts), env, nil
}
