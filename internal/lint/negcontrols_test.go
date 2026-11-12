package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// repeatedHeadings returns a message for each level-2 heading of a Markdown
// text that an earlier one already used, with both line numbers. A heading
// inside a fenced code block is not a heading.
func repeatedHeadings(text string) []string {
	var out []string
	seen := map[string]int{}
	fence := regexp.MustCompile("^(`{3,}|~{3,})(.*)$")
	var open string
	for n, line := range strings.Split(text, "\n") {
		n++
		if m := fence.FindStringSubmatch(line); m != nil {
			switch {
			case open == "":
				open = m[1]
			case m[1][0] == open[0] && len(m[1]) >= len(open) && strings.TrimSpace(m[2]) == "":
				open = ""
			}
			continue
		}
		if open != "" || !strings.HasPrefix(line, "## ") {
			continue
		}
		h := strings.TrimSpace(line)
		if first, ok := seen[h]; ok {
			out = append(out, h+" is at line "+strconv.Itoa(first)+" and again at line "+strconv.Itoa(n))
			continue
		}
		seen[h] = n
	}
	return out
}

// TestNegativeControlsHeadingsAreUnique holds e2e/tui/NEGATIVE_CONTROLS.md to
// one section for each heading. The file takes merge=union so that branches
// which each add a record do not conflict, and a union merge keeps both sides
// of a hunk both branches wrote: a section two branches carried (a record
// that was rebased, or a heading both added) came out twice. Fifteen had, the
// copies of one test's record drifting apart. Merge the records into one
// section.
func TestNegativeControlsHeadingsAreUnique(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "e2e", "tui", "NEGATIVE_CONTROLS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range repeatedHeadings(string(data)) {
		t.Errorf("NEGATIVE_CONTROLS.md: %s: keep one section, with every record in it", msg)
	}
}

// TestRepeatedHeadingLintCatchesEachForm keeps the check honest: a repeat is
// caught, a repeat after other sections is caught, and a heading-looking line
// in a fenced block, a level-3 heading used twice and the same words as a
// level-2 and a level-3 are not repeats.
func TestRepeatedHeadingLintCatchesEachForm(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       int
	}{
		{"back to back", "## A\n\ntext\n\n## A\n", 1},
		{"far apart", "## A\n\n## B\n\n## C\n\n## A\n", 1},
		{"three copies", "## A\n## A\n## A\n", 2},
		{"a heading in a fenced block", "## A\n\n```\n## A\n```\n\n## B\n", 0},
		{"a longer fence closes only on its own length", "````\n```\n## A\n````\n## A\n## A\n", 1},
		{"level 3 may repeat under other sections", "## A\n### Review\n## B\n### Review\n", 0},
		{"level 2 and level 3 of the same words", "## A\n### A\n", 0},
		{"distinct headings", "## A\n## B\n", 0},
	} {
		if got := repeatedHeadings(tc.text); len(got) != tc.want {
			t.Errorf("%s: %d repeats %v, want %d", tc.name, len(got), got, tc.want)
		}
	}
}
