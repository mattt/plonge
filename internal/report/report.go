// Package report turns scan results into a table or JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/scan"
)

// Cache is one cache directory in the report.
type Cache struct {
	Path         string     `json:"path"`
	Bytes        int64      `json:"bytes"`
	Project      string     `json:"project"`
	LastActivity *time.Time `json:"last_activity"`
	// ActivitySource names the source that set LastActivity, or is empty.
	ActivitySource string `json:"activity_source"`
	Quiet          bool   `json:"quiet"`
	Busy           bool   `json:"busy"`
	Rule           string `json:"rule"`
}

// SkippedRoot is a root that plonge did not scan.
type SkippedRoot struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Totals sums the caches in the report.
type Totals struct {
	Caches      int   `json:"caches"`
	Bytes       int64 `json:"bytes"`
	QuietCaches int   `json:"quiet_caches"`
	QuietBytes  int64 `json:"quiet_bytes"`
}

// Report is the data that plonge scan prints.
type Report struct {
	GeneratedAt  time.Time     `json:"generated_at"`
	QuietHours   float64       `json:"quiet_hours"`
	Caches       []Cache       `json:"caches"`
	Totals       Totals        `json:"totals"`
	Roots        int           `json:"roots"`
	SkippedRoots []SkippedRoot `json:"skipped_roots"`
	BrokenLinks  int           `json:"broken_links"`
	Errors       []string      `json:"errors"`
	// ActivitySources reports each source of agent activity.
	ActivitySources []scan.Source `json:"activity_sources"`
	// Global lists the global caches; it is nil unless they were requested.
	Global []global.Entry `json:"global,omitempty"`
}

// Build makes a report from scan results and the size of each candidate.
// sizes holds one slice per root, in the order of the candidates.
func Build(roots []scan.Root, sizes [][]int64, quietHours float64, brokenLinks int, now time.Time) Report {
	r := Report{
		GeneratedAt:  now,
		QuietHours:   quietHours,
		Caches:       []Cache{},
		Roots:        len(roots),
		SkippedRoots: []SkippedRoot{},
		BrokenLinks:  brokenLinks,
		Errors:       []string{},

		ActivitySources: []scan.Source{},
	}
	for i, root := range roots {
		if root.Skipped != "" {
			r.SkippedRoots = append(r.SkippedRoots, SkippedRoot{Path: root.Path, Reason: root.Skipped})
			continue
		}
		r.Errors = append(r.Errors, root.Errors...)
		var last *time.Time
		if !root.LastActivity.IsZero() {
			t := root.LastActivity
			last = &t
		}
		quiet := scan.Quiet(root.LastActivity, now, quietHours)
		for j, c := range root.Candidates {
			cache := Cache{
				Path:         c.Path,
				Bytes:        sizes[i][j],
				Project:      root.Path,
				LastActivity: last,
				Quiet:        quiet && !root.Busy,
				Rule:         c.Rule,

				ActivitySource: root.ActivitySource,
				Busy:           root.Busy,
			}
			r.Caches = append(r.Caches, cache)
			r.Totals.Caches++
			r.Totals.Bytes += cache.Bytes
			if cache.Quiet {
				r.Totals.QuietCaches++
				r.Totals.QuietBytes += cache.Bytes
			}
		}
	}
	sort.SliceStable(r.Caches, func(a, b int) bool {
		ca, cb := r.Caches[a], r.Caches[b]
		if ca.Bytes != cb.Bytes {
			return ca.Bytes > cb.Bytes
		}
		ta, tb := activityKey(ca), activityKey(cb)
		if !ta.Equal(tb) {
			return ta.Before(tb) // older first
		}
		return ca.Path < cb.Path
	})
	return r
}

// activityKey sorts an unknown activity time before all others.
func activityKey(c Cache) time.Time {
	if c.LastActivity == nil {
		return time.Time{}
	}
	return *c.LastActivity
}

// WriteJSON prints the report as indented JSON.
func WriteJSON(w io.Writer, r any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteTable prints the report as a table followed by totals.
// Paths inside home are shown with ~.
func WriteTable(w io.Writer, r Report, home string) error {
	if len(r.Caches) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tSIZE\tPROJECT\tLAST ACTIVITY\tSOURCE\tQUIET\tRULE")
		for _, c := range r.Caches {
			quiet := "no"
			if c.Busy {
				quiet = "busy"
			} else if c.Quiet {
				quiet = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				Tilde(c.Path, home), HumanBytes(c.Bytes), Tilde(c.Project, home),
				Age(c.LastActivity, r.GeneratedAt), c.ActivitySource, quiet, c.Rule)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}
	t := r.Totals
	fmt.Fprintf(w, "Total: %s in %s\n", HumanBytes(t.Bytes), plural(t.Caches, "cache"))
	fmt.Fprintf(w, "Quiet for %s or more: %s in %s\n",
		hours(r.QuietHours), HumanBytes(t.QuietBytes), plural(t.QuietCaches, "cache"))
	fmt.Fprintf(w, "Roots: %d scanned, %d skipped\n", r.Roots-len(r.SkippedRoots), len(r.SkippedRoots))
	fmt.Fprintf(w, "Broken mise links: %d\n", r.BrokenLinks)
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "Git errors: %d (caches in those repositories are not listed)\n", len(r.Errors))
	}
	writeSkippedSources(w, r.ActivitySources)
	if r.Global != nil {
		if err := writeGlobal(w, r.Global, home); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "Plonge only reports; it deleted nothing.")
	return err
}

func writeSkippedSources(w io.Writer, sources []scan.Source) {
	for _, s := range sources {
		if s.Status == scan.SourceSkipped {
			fmt.Fprintf(w, "Activity source skipped: %s (%s)\n", s.Name, s.Reason)
		}
	}
}

func writeGlobal(w io.Writer, entries []global.Entry, home string) error {
	fmt.Fprintln(w, "\nGlobal caches:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSIZE\tKIND\tPATH\tCOMMAND")
	var total int64
	n := 0
	for _, e := range entries {
		if e.Skipped != "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			e.ID, HumanBytes(e.Bytes), e.Kind, Tilde(e.Path, home), e.Command)
		total += e.Bytes
		n++
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, e := range entries {
		if e.Skipped != "" {
			fmt.Fprintf(w, "Skipped %s: %s\n", e.ID, e.Skipped)
		}
	}
	_, err := fmt.Fprintf(w, "Global total: %s in %s\n", HumanBytes(total), plural(n, "rule"))
	return err
}

// WriteGlobalClean prints the outcome of a global clean and ends with the space freed.
func WriteGlobalClean(w io.Writer, r global.Result, home string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSIZE\tKIND\tCOMMAND\tSTATUS")
	for _, o := range r.Outcomes {
		status := o.Status
		if o.Reason != "" {
			status += ": " + o.Reason
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", o.ID, HumanBytes(o.Bytes), o.Kind, o.Command, status)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	if r.Failed > 0 {
		fmt.Fprintf(w, "Failed: %d\n", r.Failed)
	}
	if r.Applied {
		_, err := fmt.Fprintf(w, "Freed: %s\n", HumanBytes(r.FreedBytes))
		return err
	}
	_, err := fmt.Fprintln(w, "Dry run; no command ran. Use --apply to run them.")
	return err
}

// WriteClean prints the outcome of a clean and ends with the space freed.
func WriteClean(w io.Writer, r clean.Result, home string) error {
	if len(r.Items) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tSIZE\tPROJECT\tRULE\tSTATUS")
		for _, it := range r.Items {
			status := it.Status
			if it.Reason != "" {
				status += ": " + it.Reason
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				Tilde(it.Path, home), HumanBytes(it.Bytes), Tilde(it.Project, home), it.Rule, status)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}
	n := r.NotSelected
	if n.Busy.Caches > 0 {
		fmt.Fprintf(w, "Not selected, an agent is working there: %s in %s\n",
			HumanBytes(n.Busy.Bytes), plural(n.Busy.Caches, "cache"))
	}
	if n.Active.Caches > 0 {
		fmt.Fprintf(w, "Not selected, active project: %s in %s (use --include-active)\n",
			HumanBytes(n.Active.Bytes), plural(n.Active.Caches, "cache"))
	}
	if n.BelowSize.Caches > 0 {
		fmt.Fprintf(w, "Not selected, below --min-size: %s in %s\n",
			HumanBytes(n.BelowSize.Bytes), plural(n.BelowSize.Caches, "cache"))
	}
	if r.Skipped > 0 {
		fmt.Fprintf(w, "Skipped: %d\n", r.Skipped)
	}
	if r.Failed > 0 {
		fmt.Fprintf(w, "Failed: %d\n", r.Failed)
	}
	writeSkippedSources(w, r.ActivitySources)
	var err error
	if r.Applied {
		_, err = fmt.Fprintf(w, "Freed: %s\n", HumanBytes(r.FreedBytes))
	} else {
		fmt.Fprintf(w, "Would delete: %s in %s\n", HumanBytes(r.Selected.Bytes), plural(r.Selected.Caches, "cache"))
		_, err = fmt.Fprintln(w, "Dry run; nothing was deleted. Use --apply to delete.")
	}
	return err
}

// Tilde replaces home at the start of path with ~.
func Tilde(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// HumanBytes formats n with binary units, as du -h does.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// Age formats how long before now t was.
func Age(t *time.Time, now time.Time) string {
	if t == nil {
		return "unknown"
	}
	d := now.Sub(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func hours(h float64) string {
	return fmt.Sprintf("%gh", h)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
