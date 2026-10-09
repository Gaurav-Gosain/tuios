package webshell

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// runAt runs one command line in a shell whose pane is cols wide and
// returns what it printed.
func runAt(t *testing.T, cols int, cwd, line string) string {
	t.Helper()
	p := NewPty(cols, 30)
	tty := newTTY(p, nil)
	defer tty.stop()
	// A full-screen program draws a frame and then waits for a key.
	_, _ = p.Write([]byte("q"))
	s := &shell{t: tty, cwd: cwd}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(line)
		_ = p.out.Close()
	}()
	out, _ := io.ReadAll(p)
	<-done
	return string(out)
}

// TestListingsFitThePane checks that listings and tables never print a line
// wider than the pane. A wider line wraps, which breaks a name in two: the
// hero on /learn showed ls print "to" on one line and "do.md" on the next.
// cat and the pager print a file as it is, so they are not here.
func TestListingsFitThePane(t *testing.T) {
	lines := []struct{ cwd, line string }{
		{Home, "ls"},
		{Home, "ls -a"},
		{Home, "ls projects/hello"},
		{Home, "help"},
		{Home, "git"},
		{Home, "tuios"},
		{Home, "tuios ls"},
		{Home, "tuios list-agents"},
		{Home, "tuios list-hooks"},
		{Home, "tuios list-verbs"},
		{Home, "tuios fan"},
		{Home, "tuios tape list"},
		{Home, "colors"},
		{Home, "fortune"},
		{Home, "cowsay hello"},
		{Home, "cowsay a cow with a great deal to say about window managers in a browser tab"},
		{Home, "tree"},
		{Home, "top"},
		{Home, "fastfetch"},
		{ProjectDir, "git status"},
		{ProjectDir, "git log --oneline"},
	}
	for _, cols := range []int{40, 57, 80, 120} {
		for _, l := range lines {
			out := runAt(t, cols, l.cwd, l.line)
			for i, row := range strings.Split(out, "\r\n") {
				if w := ansi.StringWidth(row); w > cols {
					t.Errorf("cols %d, %q: line %d is %d cells wide: %q", cols, l.line, i, w, ansi.Strip(row))
				}
			}
		}
	}
}

// TestLsKeepsNamesWhole checks that ls prints every name whole, in columns
// that line up, at every pane width.
func TestLsKeepsNamesWhole(t *testing.T) {
	var names []string
	for _, e := range list(Home) {
		if !strings.HasPrefix(e.name, ".") {
			if e.dir {
				e.name += "/"
			}
			names = append(names, e.name)
		}
	}
	for _, cols := range []int{40, 57, 80, 120} {
		out := ansi.Strip(runAt(t, cols, Home, "ls"))
		var got []string
		for row := range strings.SplitSeq(strings.TrimSuffix(out, "\r\n"), "\r\n") {
			got = append(got, strings.Fields(row)...)
		}
		if strings.Join(got, " ") == strings.Join(names, " ") {
			// Row-major order would match too. ls is column-major, so with
			// more than one row the first column holds the first names.
			if strings.Count(out, "\r\n") > 1 {
				t.Errorf("cols %d: names run across the rows, not down the columns:\n%s", cols, out)
			}
		}
		whole := len(got) == len(names)
		for _, n := range names {
			if !strings.Contains(" "+strings.Join(got, " ")+" ", " "+n+" ") {
				whole = false
			}
		}
		if !whole {
			t.Errorf("cols %d: want every name whole %v, got:\n%s", cols, names, out)
		}
	}
}
