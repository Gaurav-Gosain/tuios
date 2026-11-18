package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestCLIReferenceHasASectionPerCommand fails on a top-level command that has
// no heading of its own in docs/CLI_REFERENCE.md. TestCLIReferenceNamesEveryCommandAndFlag
// is satisfied by a table row; this one is not, because tuios.dev renders the
// reference from this file and a guide links to a command by its heading.
//
// A heading covers a command when one of its code spans starts with the
// command's name, with or without "tuios " in front: "`tuios cp`" covers cp,
// "`tuios hosts test`" covers hosts, and "`show-buffer`" in a heading that
// lists several commands covers show-buffer.
func TestCLIReferenceHasASectionPerCommand(t *testing.T) {
	doc := readDoc(t, "CLI_REFERENCE.md")
	heading := regexp.MustCompile("^#{1,6} ")
	span := regexp.MustCompile("`([^`]+)`")
	covered := map[string]bool{}
	fenced := false
	for l := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(l, "```") {
			fenced = !fenced
		}
		if fenced || !heading.MatchString(l) {
			continue
		}
		for _, m := range span.FindAllStringSubmatch(l, -1) {
			fields := strings.Fields(strings.TrimPrefix(m[1], "tuios "))
			if len(fields) > 0 {
				covered[fields[0]] = true
			}
		}
	}

	for _, d := range commandDocs(newRootCommand(), true) {
		if d.Depth != 1 {
			continue
		}
		name := strings.TrimPrefix(d.Path, "tuios ")
		if name == "help" {
			continue
		}
		if !covered[name] {
			t.Errorf("CLI_REFERENCE.md has no heading for %q. Add a section with \"### `%s`\"", d.Path, d.Path)
		}
	}
}
