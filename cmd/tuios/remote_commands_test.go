package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestPrintOptionListGroupsEachSectionOnce. A section is a display grouping,
// not a path prefix: appearance.clock_format and appearance.show_cpu are both
// section "dock". The daemon reports the registry in path order, so printing a
// heading whenever the section changes scatters a section over the page instead
// of grouping it.
//
// Negative control: drop the slices.SortFunc call from printOptionList and this
// fails, once for every section the path order crosses back into.
func TestPrintOptionListGroupsEachSectionOnce(t *testing.T) {
	options := make([]optionRow, 0, len(config.Options()))
	section := map[string]string{}
	seen := map[string]bool{}
	for _, opt := range config.Options() {
		options = append(options, optionRow{Path: opt.Path, Type: opt.Type, Section: opt.Section})
		section[opt.Path] = opt.Section
		seen[opt.Section] = true
	}

	var buf bytes.Buffer
	printOptionList(&buf, options, sortedSections(seen), len(options))

	headings := map[string]int{}
	under := map[string][]string{}
	current := ""
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			headings[current]++
			continue
		}
		if path, _, ok := strings.Cut(strings.TrimSpace(line), " "); ok && section[path] != "" {
			under[current] = append(under[current], path)
		}
	}

	for name, count := range headings {
		if count != 1 {
			t.Errorf("[%s] printed %d times, want 1", name, count)
		}
	}
	if len(headings) != len(seen) {
		t.Fatalf("printed %d headings for %d sections", len(headings), len(seen))
	}
	// The positive half. One heading each would also be satisfied by a listing
	// that dropped rows, so every path has to turn up exactly once, and under
	// the section the registry files it in rather than the one it starts with.
	printed := map[string]int{}
	for name, paths := range under {
		for _, path := range paths {
			printed[path]++
			if section[path] != name {
				t.Errorf("%s is listed under [%s], want [%s]", path, name, section[path])
			}
		}
	}
	for path := range section {
		if printed[path] != 1 {
			t.Errorf("%s printed %d times, want 1", path, printed[path])
		}
	}
}

// sortedSections returns the section names the way the daemon reports them.
func sortedSections(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
