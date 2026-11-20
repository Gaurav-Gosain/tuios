package session

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRestrictedTableNamesEveryVerbItAllows holds the table of what a
// restricted connection may call, in docs/protocol.md, to the scope each verb
// names in the registry, which is what checkScope decides from. Every verb a
// restricted connection may call is named once, in the row of its scope, and
// the table names nothing else, so a new verb cannot reach the daemon and
// miss the page.
func TestRestrictedTableNamesEveryVerbItAllows(t *testing.T) {
	data, err := os.ReadFile("../../docs/protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), "\nWhat a restricted connection may call:\n")
	if !ok {
		t.Fatal(`protocol.md has no line "What a restricted connection may call:" before the table of restrict-connection`)
	}
	var lines []string
	for line := range strings.SplitSeq(rest, "\n") {
		if !strings.HasPrefix(line, "|") {
			if len(lines) == 0 && strings.TrimSpace(line) == "" {
				continue
			}
			break
		}
		lines = append(lines, line)
	}
	if len(lines) < 3 {
		t.Fatal(`protocol.md has no table rows after "What a restricted connection may call:"`)
	}

	// The row of each scope. A write verb that reads the paste buffers has a
	// row of its own: under own it is refused as a buffer read is.
	type rowKey struct {
		kind    scopeKind
		buffers bool
	}
	rowFor := map[rowKey]string{
		{scopeOpen, false}:        "open",
		{scopeGlobal, false}:      "across sessions",
		{scopeRead, false}:        "read one session",
		{scopeSelf, false}:        "own pane's record",
		{scopeMail, false}:        "mail and stash",
		{scopeWrite, false}:       "type into a pane",
		{scopeWrite, true}:        "paste a buffer",
		{scopeLaunch, false}:      "start sessions",
		{scopeBufferRead, true}:   "read the paste buffers",
		{scopeBufferWrite, false}: "change the paste buffers",
		{scopeTask, false}:        "task cards",
	}
	const restRow = "everything else"
	classes := map[string]bool{}
	for _, class := range rowFor {
		classes[class] = true
	}

	reg := verbRegistry()
	var errs []string
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}
	const table = "The table of what a restricted connection may call in protocol.md"

	namedIn := map[string][]string{}
	for _, line := range lines[2:] {
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		class := strings.TrimSpace(cells[1])
		var names []string
		if len(cells) > 2 {
			spans := strings.Split(cells[2], "`")
			for i := 1; i < len(spans)-1; i += 2 {
				names = append(names, spans[i])
			}
		}
		if class != restRow && !classes[class] {
			fail("%s has a row %q, which is no class of a verb's scope. Name the row as its scope's class, or move its verbs to their rows", table, class)
		}
		for _, name := range names {
			namedIn[name] = append(namedIn[name], class)
			if _, ok := reg[name]; !ok {
				fail("%s names %q in the row %q, which is not a verb. Remove it", table, name, class)
			} else if class == restRow {
				fail("%s names %q in the row %q, which names no verb. Move it to the row of its scope", table, name, restRow)
			}
		}
	}

	for name := range reg {
		kind, _ := verbScope(name)
		rows := namedIn[name]
		if kind == scopeDeny {
			if len(rows) > 0 {
				fail("%s names %q (rows %q), which a restricted connection may not call. Remove it, or give the verb a scope", table, name, rows)
			}
			continue
		}
		want := rowFor[rowKey{kind, readsBuffers(name)}]
		if len(rows) == 0 {
			fail("%s does not name %q. Add it to the row %q", table, name, want)
			continue
		}
		for _, row := range rows {
			if row != want {
				fail("%s names %q in the row %q, and its scope puts it in the row %q. Move it", table, name, row, want)
			}
		}
		if len(rows) > 1 {
			fail("%s names %q %d times (rows %q). Name it once, in the row %q", table, name, len(rows), rows, want)
		}
	}

	slices.Sort(errs)
	for _, e := range errs {
		t.Errorf("%s", e)
	}
}
