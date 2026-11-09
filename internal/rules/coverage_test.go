package rules

import (
	"os"
	"path/filepath"
	"regexp"
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
	code := regexp.MustCompile("`([^`]+)`")
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		for _, match := range code.FindAllStringSubmatch(line, -1) {
			listed[match[1]] = true
		}
	}
	r := Default()
	for _, name := range r.Names {
		if !listed[name] {
			t.Errorf("coverage table is missing default cache name %q", name)
		}
	}
	for _, rule := range r.Global {
		if !listed[rule.ID] {
			t.Errorf("coverage table is missing default global rule ID %q", rule.ID)
		}
	}
	if !listed[XcodeRuleID] {
		t.Errorf("coverage table is missing built-in global rule ID %q", XcodeRuleID)
	}
}
