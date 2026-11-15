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
					if strings.HasPrefix(body, "path =") || strings.HasPrefix(body, "refuse_names =") {
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
