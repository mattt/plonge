package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDocumentedRules(t *testing.T) {
	blocks := regexp.MustCompile("(?s)```toml\\n(.*?)\\n```")
	for _, name := range []string{"README.md", "docs/rules.md"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", name))
			if err != nil {
				t.Fatal(err)
			}
			examples := blocks.FindAllStringSubmatch(string(data), -1)
			if len(examples) == 0 {
				t.Fatal("no TOML examples found")
			}
			for i, example := range examples {
				t.Run(fmt.Sprint(i+1), func(t *testing.T) {
					body := example[1]
					// Field-only snippets describe part of a global rule.
					// Decode them into Global so unknown fields cannot pass silently.
					if strings.HasPrefix(body, "path =") || strings.HasPrefix(body, "refuse_names =") ||
						strings.HasPrefix(body, "keep_names =") {
						var g Global
						md, err := toml.Decode(body, &g)
						if err != nil {
							t.Fatal(err)
						}
						if keys := md.Undecoded(); len(keys) != 0 {
							t.Fatalf("unknown global fields: %v", keys)
						}
						return
					}
					if _, err := Load(writeRules(t, body)); err != nil {
						t.Fatal(err)
					}
				})
			}
			if name == "docs/rules.md" {
				// The first example is the default project configuration.
				got, err := parse(examples[0][1], name)
				if err != nil {
					t.Fatal(err)
				}
				want, err := parse(defaultTOML, "default rules")
				if err != nil {
					t.Fatal(err)
				}
				want.Global = nil
				if !reflect.DeepEqual(got, want) {
					t.Error("documented project defaults differ from default.toml")
				}
			}
		})
	}
}

// TestDocumentedLinks checks that each relative link in the documents
// names a file that exists and, with a fragment, a heading in that file.
func TestDocumentedLinks(t *testing.T) {
	root := filepath.Join("..", "..")
	links := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headings := regexp.MustCompile(`(?m)^#+ +(.+)$`)
	fences := regexp.MustCompile("(?s)```.*?```")
	dropped := regexp.MustCompile(`[^a-z0-9 _-]`)
	anchors := func(t *testing.T, path string) map[string]bool {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, m := range headings.FindAllStringSubmatch(fences.ReplaceAllString(string(data), ""), -1) {
			// GitHub lowercases a heading, drops punctuation, and hyphenates spaces.
			slug := dropped.ReplaceAllString(strings.ToLower(strings.TrimSpace(m[1])), "")
			found[strings.ReplaceAll(slug, " ", "-")] = true
		}
		return found
	}
	for _, name := range []string{"README.md", "docs/rules.md", "docs/coverage.md"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range links.FindAllStringSubmatch(fences.ReplaceAllString(string(data), ""), -1) {
				link := m[1]
				if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
					continue
				}
				file, fragment, _ := strings.Cut(link, "#")
				target := filepath.Join(root, name)
				if file != "" {
					target = filepath.Join(root, filepath.Dir(name), file)
				}
				if _, err := os.Stat(target); err != nil {
					t.Errorf("link %s: %v", link, err)
					continue
				}
				if fragment != "" && !anchors(t, target)[fragment] {
					t.Errorf("link %s: no heading for #%s", link, fragment)
				}
			}
		})
	}
}
