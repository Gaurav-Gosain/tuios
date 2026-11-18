package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestConfigurationHasASectionPerTable fails on a top-level table of
// config.toml that has no heading in docs/CONFIGURATION.md. tuios.dev renders
// the page from this file, and a guide links to a table by its heading.
//
// A heading covers a table when it names it in a code span: "`[daemon]`",
// "`[[actions]]`" or "`[hosts.NAME]`".
func TestConfigurationHasASectionPerTable(t *testing.T) {
	data, err := os.ReadFile("../../docs/CONFIGURATION.md")
	if err != nil {
		t.Fatal(err)
	}
	span := regexp.MustCompile("`\\[\\[?([a-z_]+)[\\].]")
	covered := map[string]bool{}
	fenced := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		for _, m := range span.FindAllStringSubmatch(line, -1) {
			covered[m[1]] = true
		}
	}

	for field := range reflect.TypeFor[UserConfig]().Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		if name == "" || name == "-" {
			continue
		}
		if !covered[name] {
			t.Errorf("CONFIGURATION.md has no heading for the [%s] table. Add a section named \"The `[%s]` table\"", name, name)
		}
	}
}
