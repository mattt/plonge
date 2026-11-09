// Package clean deletes the cache directories that a scan found.
package clean

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mattt/plonge/internal/scan"
)

// The statuses of an Item.
const (
	WouldDelete = "would delete"
	Deleted     = "deleted"
	Skipped     = "skipped"
	Failed      = "failed"
)

// Options select the caches to delete.
type Options struct {
	Apply         bool // delete; without it, only report
	IncludeActive bool // also take caches in roots that are not quiet
	MinSize       int64
	QuietHours    float64
	Now           time.Time
}

// Item is one selected cache and what happened to it.
type Item struct {
	Path       string `json:"path"`
	Project    string `json:"project"`
	Rule       string `json:"rule"`
	Bytes      int64  `json:"bytes"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	FreedBytes int64  `json:"freed_bytes"`
}

// Group counts caches and their size.
type Group struct {
	Caches int   `json:"caches"`
	Bytes  int64 `json:"bytes"`
}

func (g *Group) add(n int64) {
	g.Caches++
	g.Bytes += n
}

// NotSelected counts the caches that the options left out.
type NotSelected struct {
	Active    Group `json:"active"`
	BelowSize Group `json:"below_size"`
}

// Result is the outcome of a clean.
type Result struct {
	Applied     bool        `json:"applied"`
	Items       []Item      `json:"items"`
	Selected    Group       `json:"selected"`
	NotSelected NotSelected `json:"not_selected"`
	FreedBytes  int64       `json:"freed_bytes"`
	Skipped     int         `json:"skipped"`
	Failed      int         `json:"failed"`
	Errors      []string    `json:"errors"`
}

type planned struct {
	item Item
	root string
	cand scan.Candidate
}

// Run selects caches from the scan results and, with Apply, deletes them.
// sizes holds one slice per root, in the order of the candidates.
func Run(ctx context.Context, roots []scan.Root, sizes [][]int64, opts Options) Result {
	res := Result{Applied: opts.Apply, Items: []Item{}, Errors: []string{}}
	var plan []planned
	for i, root := range roots {
		if root.Skipped != "" {
			continue
		}
		res.Errors = append(res.Errors, root.Errors...)
		quiet := scan.Quiet(root.LastActivity, opts.Now, opts.QuietHours)
		for j, c := range root.Candidates {
			n := sizes[i][j]
			switch {
			case !quiet && !opts.IncludeActive:
				res.NotSelected.Active.add(n)
				continue
			case n < opts.MinSize:
				res.NotSelected.BelowSize.add(n)
				continue
			}
			res.Selected.add(n)
			plan = append(plan, planned{
				item: Item{Path: c.Path, Project: root.Path, Rule: c.Rule, Bytes: n, Status: WouldDelete},
				root: root.Path,
				cand: c,
			})
		}
	}
	sort.SliceStable(plan, func(a, b int) bool {
		if plan[a].item.Bytes != plan[b].item.Bytes {
			return plan[a].item.Bytes > plan[b].item.Bytes
		}
		return plan[a].item.Path < plan[b].item.Path
	})
	for _, p := range plan {
		if opts.Apply {
			apply(ctx, &p)
		}
		res.Items = append(res.Items, p.item)
		res.FreedBytes += p.item.FreedBytes
		switch p.item.Status {
		case Skipped:
			res.Skipped++
		case Failed:
			res.Failed++
		}
	}
	return res
}

func apply(ctx context.Context, p *planned) {
	reason, err := scan.Verify(ctx, p.root, p.cand)
	if err != nil {
		p.item.Status, p.item.Reason = Failed, err.Error()
		return
	}
	if reason != "" {
		p.item.Status, p.item.Reason = Skipped, reason
		return
	}
	if err := RemoveTree(p.item.Path); err != nil {
		left := scan.DiskUsage(p.item.Path)
		p.item.Status, p.item.Reason = Failed, err.Error()
		p.item.FreedBytes = max(0, p.item.Bytes-left)
		return
	}
	p.item.Status = Deleted
	p.item.FreedBytes = p.item.Bytes
}

// RemoveTree deletes path and everything under it.
// When permissions stop the removal, as read-only directories in
// Go's module cache do, it makes the tree writable and tries again.
// It does not follow symlinks.
func RemoveTree(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	makeWritable(path)
	return os.RemoveAll(path)
}

func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		need := fs.FileMode(0o200)
		if d.IsDir() {
			need = 0o700
		}
		if perm := info.Mode().Perm(); perm&need != need {
			_ = os.Chmod(path, perm|need)
		}
		return nil
	})
}
