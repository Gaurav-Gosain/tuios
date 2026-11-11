package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestCLIReferenceNamesEveryCommandAndFlag walks the real command tree, the one
// tuios help --json prints, and fails on a command or a flag docs/CLI_REFERENCE.md
// does not name. TestAgentGuideCommandsResolve checks the other direction (a
// command in the guide exists); this one is what catches a command added without
// its reference.
//
// A command is named when its path appears in the file. A flag the command
// shares with two or more others (--json, --session, the interface flags of
// attach, new and ssh) is named when --name appears anywhere in the file: the
// reference documents those once, where they belong. A flag of one or two
// commands has to appear in the command's own section, the text from the heading
// that carries its path to the next heading of the same or a higher level; a
// command with no heading of its own is looked for in the whole file.
func TestCLIReferenceNamesEveryCommandAndFlag(t *testing.T) {
	doc := readDoc(t, "CLI_REFERENCE.md")
	lines := strings.Split(doc, "\n")
	heading := regexp.MustCompile("^(#{1,6}) ")
	// A line starting with # inside a code fence is a shell comment, not a
	// heading.
	isHeading := make([]bool, len(lines))
	fenced := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			fenced = !fenced
		}
		isHeading[i] = !fenced && heading.MatchString(l)
	}

	// sectionOf returns the lines under the heading that names path, and
	// whether there is one.
	sectionOf := func(path string) (string, bool) {
		for i, l := range lines {
			if !isHeading[i] || !strings.Contains(l, "`"+path+"`") {
				continue
			}
			m := heading.FindStringSubmatch(l)
			level := len(m[1])
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if isHeading[j] && len(heading.FindStringSubmatch(lines[j])[1]) <= level {
					end = j
					break
				}
			}
			return strings.Join(lines[i:end], "\n"), true
		}
		return "", false
	}

	docs := commandDocs(newRootCommand())
	owners := map[string]int{}
	for _, d := range docs {
		for _, f := range d.Flags {
			owners[f.Name]++
		}
	}
	for _, d := range docs {
		if d.Depth == 0 || d.Path == "tuios help" || strings.HasPrefix(d.Path, "tuios help ") {
			continue
		}
		if !strings.Contains(doc, d.Path) {
			t.Errorf("CLI_REFERENCE.md never names the command %q", d.Path)
			continue
		}
		scope, ok := sectionOf(d.Path)
		if !ok {
			scope = doc
		}
		for _, f := range d.Flags {
			where := scope
			if owners[f.Name] > 2 {
				where = doc
			}
			if !strings.Contains(where, "--"+f.Name) {
				t.Errorf("CLI_REFERENCE.md does not name %s --%s", d.Path, f.Name)
			}
		}
	}
}
