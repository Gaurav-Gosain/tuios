package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
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
	isHeading := make([]bool, len(lines))
	fenced := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			fenced = !fenced
		}
		isHeading[i] = !fenced && heading.MatchString(l)
	}

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

	docs := commandDocs(newRootCommand(), false)
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

type cliReferenceSection struct {
	heading  string
	commands []*cobra.Command
	lines    []cliReferenceLine
}

type cliReferenceLine struct {
	n    int
	text string
}

func cliReferenceSections(t *testing.T, root *cobra.Command, bad func(line int, msg string)) []*cliReferenceSection {
	t.Helper()
	span := regexp.MustCompile("`(tuios(?: [^`]*)?)`")
	find := func(line string) (*cobra.Command, bool) {
		var words []string
		for _, w := range strings.Fields(line)[1:] {
			if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "<[|(\"'$=") {
				break
			}
			words = append(words, w)
		}
		c, _, err := root.Find(words)
		return c, err == nil
	}
	var out []*cliReferenceSection
	var cur *cliReferenceSection
	fenced := false
	for i, line := range strings.Split(readDoc(t, "CLI_REFERENCE.md"), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "#") {
			cur = &cliReferenceSection{heading: line}
			out = append(out, cur)
			for _, m := range span.FindAllStringSubmatch(line, -1) {
				c, ok := find(m[1])
				if !ok || c == root && m[1] != "tuios" {
					bad(i+1, "the heading names "+m[1]+", which is not a command")
					continue
				}
				cur.commands = append(cur.commands, c)
			}
			continue
		}
		if cur == nil {
			continue
		}
		if fenced {
			if strings.HasPrefix(line, "tuios ") {
				if c, ok := find(line); ok && c != root {
					cur.commands = append(cur.commands, c)
				}
			}
			continue
		}
		cur.lines = append(cur.lines, cliReferenceLine{n: i + 1, text: line})
	}
	return out
}

// TestCLIReferenceDocumentsOnlyRealCommandsAndFlags is the other direction of
// TestCLIReferenceNamesEveryCommandAndFlag. It fails on a heading that names a
// command the tree does not have, and on a flag that a command's section
// lists, as "- `--name`" or "| `--name` |", that the command does not
// take. A command or a flag that was removed or renamed keeps its text
// otherwise.
//
// It also holds a default the section states, "(default: v)", to the flag's
// default in the code, when the code has one and the page gives a value
// rather than a description such as "the focused pane".
func TestCLIReferenceDocumentsOnlyRealCommandsAndFlags(t *testing.T) {
	root := newRootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	bad := func(line int, msg string) { t.Errorf("CLI_REFERENCE.md:%d: %s", line, msg) }
	listed := regexp.MustCompile(`^\s*(?:[-*] |\| )` + "`" + `(?:-[a-zA-Z], )?--([a-z][a-z0-9-]*)`)
	stated := regexp.MustCompile(`\(default:? ([^)]*)\)`)
	flags, defaults := 0, 0
	for _, s := range cliReferenceSections(t, root, bad) {
		if len(s.commands) == 0 {
			continue
		}
		for _, l := range s.lines {
			m := listed.FindStringSubmatch(l.text)
			if m == nil || m[1] == "help" {
				continue
			}
			flags++
			var defValue string
			found := false
			for _, c := range s.commands {
				if f := c.Flags().Lookup(m[1]); f != nil {
					defValue, found = f.DefValue, true
					break
				}
			}
			if !found {
				bad(l.n, "--"+m[1]+" is not a flag of "+s.heading)
				continue
			}
			if d := stated.FindStringSubmatch(l.text); d != nil {
				if claim, ok := literalDefault(d[1]); ok && !zeroDefault(defValue) {
					defaults++
					if !sameDefault(claim, defValue) {
						bad(l.n, "--"+m[1]+": the page says the default is "+claim+", the code has "+defValue)
					}
				}
			}
		}
	}
	if flags < 100 || defaults < 5 {
		t.Fatalf("found %d listed flags and %d defaults, want the whole page", flags, defaults)
	}
}

func literalDefault(text string) (string, bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(text), ",")
	first = strings.TrimSpace(first)
	if v, ok := strings.CutPrefix(first, "`"); ok {
		v, _, _ = strings.Cut(v, "`")
		return strings.Trim(v, `"`), true
	}
	if strings.HasPrefix(first, `"`) {
		return strings.Trim(first, `"`), true
	}
	if regexp.MustCompile(`^[0-9][0-9a-z.]*$`).MatchString(first) {
		return first, true
	}
	return "", false
}

func zeroDefault(v string) bool {
	switch v {
	case "", "0", "0s", "false", "[]":
		return true
	}
	return false
}

func sameDefault(claim, code string) bool {
	if claim == code {
		return true
	}
	a, errA := time.ParseDuration(claim)
	b, errB := time.ParseDuration(code)
	return errA == nil && errB == nil && a == b
}
