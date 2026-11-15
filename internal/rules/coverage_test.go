package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCoverageTableIncludesDefaultRules(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "coverage.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Check table cells, so a mention in prose or a source link is not enough.
	listed := map[string]bool{}
	var rows []map[string]bool
	code := regexp.MustCompile("`([^`]+)`")
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		row := map[string]bool{}
		for _, match := range code.FindAllStringSubmatch(line, -1) {
			listed[match[1]] = true
			row[match[1]] = true
		}
		rows = append(rows, row)
	}
	r := Default()
	for _, name := range r.Names {
		if !listed[name] {
			t.Errorf("coverage table is missing default cache name %q", name)
		}
	}
	for name, markers := range r.Beside {
		for _, m := range markers {
			if !listed[m] {
				t.Errorf("coverage table is missing project file %q for %q", m, name)
			}
		}
	}
	for _, rule := range r.Global {
		if !listed[rule.ID] {
			t.Errorf("coverage table is missing default global rule ID %q", rule.ID)
		}
		// A tool name can match an ID, so a row must also give the action.
		action := rule.Clean
		if rule.DeleteContents {
			action = "delete_contents"
		}
		if !slices.ContainsFunc(rows, func(row map[string]bool) bool { return row[rule.ID] && row[action] }) {
			t.Errorf("no coverage table row lists global rule %q with %q", rule.ID, action)
		}
	}
	if !listed[XcodeRuleID] {
		t.Errorf("coverage table is missing built-in global rule ID %q", XcodeRuleID)
	}
}
