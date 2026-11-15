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
	// Refused lists the caches that are or hold a Git repository.
	Refused []scan.Refusal `json:"refused"`
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
		Refused:      []scan.Refusal{},

		ActivitySources: []scan.Source{},
	}
	for i, root := range roots {
		if root.Skipped != "" {
			r.SkippedRoots = append(r.SkippedRoots, SkippedRoot{Path: root.Path, Reason: root.Skipped})
			continue
		}
		r.Errors = append(r.Errors, root.Errors...)
		r.Refused = append(r.Refused, root.Refused...)
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
		fmt.Fprintln(tw, "Path\tSize\tProject\tLast activity\tSource\tQuiet\tRule")
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
	WriteRefusals(w, r.Refused, home)
	writeSources(w, r.ActivitySources)
	if r.Global != nil {
		if err := writeGlobal(w, r.Global, home); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "Scan deleted nothing.")
	return err
}

// writeSources reports the activity sources that were not used.
// A failed source makes every project busy, so the line says how to proceed.
func writeSources(w io.Writer, sources []scan.Source) {
	for _, s := range sources {
		switch s.Status {
		case scan.SourceSkipped:
			fmt.Fprintf(w, "Activity source skipped: %s (%s)\n", s.Name, s.Reason)
		case scan.SourceFailed:
			fmt.Fprintf(w, "Activity source failed: %s (%s)\n", s.Name, s.Reason)
			fmt.Fprintf(w, "Every project counts as busy. Fix %s, or remove it from PATH for this run.\n", s.Name)
		}
	}
}

// WriteRefusals lists the caches that plonge does not report or delete
// because they are or hold a Git repository.
func WriteRefusals(w io.Writer, refused []scan.Refusal, home string) {
	for _, r := range refused {
		fmt.Fprintf(w, "Refused %s: %s\n", Tilde(r.Path, home), r.Reason)
	}
}

// writeRefused lists the files that made a rule's check refuse it.
func writeRefused(w io.Writer, e global.Entry) {
	for _, p := range e.Refused {
		fmt.Fprintf(w, "  %s\n", p)
	}
}

// writeTargets lists what a rule would remove, or says why its size is the whole folder.
func writeTargets(w io.Writer, e global.Entry) {
	if len(e.Targets) > 0 {
		fmt.Fprintf(w, "%s would remove: %s\n", e.ID, strings.Join(e.Targets, ", "))
	}
	if e.Note != "" {
		fmt.Fprintf(w, "%s: %s\n", e.ID, e.Note)
	}
}

func writeGlobal(w io.Writer, entries []global.Entry, home string) error {
	fmt.Fprintln(w, "\nGlobal caches:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSize\tKind\tPath\tAction")
	var total int64
	n := 0
	for _, e := range entries {
		if e.Skipped != "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			e.ID, globalSize(e), e.Kind, Tilde(e.Path, home), e.Command)
		total += e.Bytes
		n++
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	writeWholeCacheNote(w, entries)
	for _, e := range entries {
		if e.Skipped != "" {
			fmt.Fprintf(w, "Skipped %s: %s\n", e.ID, e.Skipped)
			writeRefused(w, e)
		}
		writeTargets(w, e)
	}
	_, err := fmt.Fprintf(w, "Global total: %s in %s\n", HumanBytes(total), plural(n, "rule"))
	return err
}

// wholeCacheLabel follows the size of a prune rule's whole cache.
const wholeCacheLabel = " (whole cache)"

// globalSize formats a rule's size and labels the size of a prune rule's
// whole cache, because the rule's tool removes only part of it.
func globalSize(e global.Entry) string {
	if e.WholeCache {
		return HumanBytes(e.Bytes) + wholeCacheLabel
	}
	return HumanBytes(e.Bytes)
}

// writeWholeCacheNote explains the label of globalSize
// when one of the entries has it.
func writeWholeCacheNote(w io.Writer, entries []global.Entry) {
	for _, e := range entries {
		if e.WholeCache && e.Skipped == "" {
			fmt.Fprintln(w, "A size marked (whole cache) is the size of a prune rule's cache.")
			fmt.Fprintln(w, "The tool removes only the part that its policy selects, which can be much less.")
			return
		}
	}
}

// WriteGlobalClean prints the outcome of a global clean and ends with the space freed.
func WriteGlobalClean(w io.Writer, r global.Result, home string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSize\tKind\tAction\tStatus")
	for _, o := range r.Outcomes {
		status := o.Status
		if o.Reason != "" {
			status += ": " + o.Reason
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", o.ID, globalSize(o.Entry), o.Kind, o.Command, status)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	entries := make([]global.Entry, len(r.Outcomes))
	for i, o := range r.Outcomes {
		entries[i] = o.Entry
	}
	writeWholeCacheNote(w, entries)
	for _, o := range r.Outcomes {
		if len(o.Refused) > 0 {
			fmt.Fprintf(w, "Skipped %s: %s\n", o.ID, o.Reason)
			writeRefused(w, o.Entry)
		}
		writeTargets(w, o.Entry)
	}
	fmt.Fprintln(w)
	if r.Failed > 0 {
		fmt.Fprintf(w, "Failed: %d\n", r.Failed)
	}
	if r.Applied {
		_, err := fmt.Fprintf(w, "Estimated freed: %s\n", HumanBytes(r.FreedBytes))
		return err
	}
	_, err := fmt.Fprintln(w, "Dry run; no cleanup action ran. Use --apply to run eligible actions.")
	return err
}

// WriteClean prints the outcome of a clean and ends with the space freed.
func WriteClean(w io.Writer, r clean.Result, home string) error {
	if len(r.Items) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "Path\tSize\tProject\tRule\tStatus")
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
		fmt.Fprintf(w, "Not selected, busy project (Paseo): %s in %s\n",
			HumanBytes(n.Busy.Bytes), plural(n.Busy.Caches, "cache"))
	}
	if n.Active.Caches > 0 {
		fmt.Fprintf(w, "Not selected, active project (recent or unknown activity): %s in %s (use --include-active)\n",
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
	WriteRefusals(w, r.Refused, home)
	writeSources(w, r.ActivitySources)
	var err error
	if r.Applied {
		_, err = fmt.Fprintf(w, "Estimated freed: %s\n", HumanBytes(r.FreedBytes))
	} else {
		fmt.Fprintf(w, "Selected size: %s in %s\n", HumanBytes(r.Selected.Bytes), plural(r.Selected.Caches, "cache"))
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
