package tuie2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

type legendSize struct {
	cols, rows int
	depth      string
	env        []string
}

func (s legendSize) name() string {
	n := fmt.Sprintf("%dx%d", s.cols, s.rows)
	if s.depth != "" {
		n += "-" + s.depth
	}
	return n
}

var legendSizes = []legendSize{
	{cols: 60, rows: 20},
	{cols: 80, rows: 24},
	{cols: 80, rows: 24, depth: "16", env: []string{"TERM=xterm", "COLORTERM="}},
	{cols: 200, rows: 50},
}

func checkKeyBold(t *testing.T, s tuitest.Screen, key, label string) {
	t.Helper()
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		line := s.Line(y)
		i := strings.Index(line, key+" "+label)
		if i < 0 {
			continue
		}
		col := len([]rune(line[:i]))
		if !s.Cell(col, y).Bold {
			t.Errorf("the key %q is not bold on the dock: %q", key, line)
		}
		if s.Cell(col+len([]rune(key))+1, y).Bold {
			t.Errorf("the label %q is bold like its key, so the two read as one: %q", label, line)
		}
		return
	}
	t.Errorf("%q %q is not on the screen", key, label)
}

type legendItem struct {
	key   string
	short []string
	label string
}

func legendVocabulary(items []legendItem) map[string]bool {
	vocab := map[string]bool{}
	for _, it := range items {
		vocab[it.key] = true
		for _, s := range it.short {
			vocab[s] = true
		}
		for w := range strings.FieldsSeq(it.label) {
			vocab[w] = true
		}
	}
	return vocab
}

func legendShown(row string, it legendItem) bool {
	for _, k := range append([]string{it.key}, it.short...) {
		if strings.Contains(row, k+" "+it.label) {
			return true
		}
	}
	return false
}

var trailRe = regexp.MustCompile(`^\d+:\d+$`)

func legendWords(row string) []string {
	fields := strings.Fields(row)
	last := -1
	for i, f := range fields {
		if trailRe.MatchString(f) {
			last = i
		}
	}
	var words []string
	for _, f := range fields[last+1:] {
		if r := []rune(f); len(r) == 1 && r[0] >= 0xe000 && r[0] <= 0xf8ff {
			continue
		}
		words = append(words, f)
	}
	return words
}

func checkLegend(t *testing.T, row string, items []legendItem, must []string, whole, all bool) {
	t.Helper()
	vocab := legendVocabulary(items)
	for _, ell := range []string{"…", "..."} {
		if strings.Contains(row, ell) {
			t.Errorf("the dock row holds an ellipsis, so something was cut: %q", row)
		}
	}
	for _, w := range legendWords(row) {
		if !vocab[w] {
			t.Errorf("the dock shows %q, which is not a whole key or label of the legend: %q", w, row)
		}
	}
	byKey := map[string]legendItem{}
	for _, it := range items {
		byKey[it.key] = it
	}
	words := map[string]bool{}
	for _, w := range legendWords(row) {
		words[w] = true
	}
	for _, k := range must {
		it := byKey[k]
		if whole && !legendShown(row, it) {
			t.Errorf("the dock lost %q %q: %q", k, it.label, row)
		}
		shown := words[it.key]
		for _, s := range it.short {
			shown = shown || words[s]
		}
		if !shown {
			t.Errorf("the dock lost the key %q: %q", k, row)
		}
	}
	if all {
		for _, it := range items {
			if !legendShown(row, it) {
				t.Errorf("the dock has room for every key and lost %q %q: %q", it.key, it.label, row)
			}
		}
	}
	if !regexp.MustCompile(`(^|\s)(\S*/)?esc(\s|$)`).MatchString(row) {
		t.Errorf("the dock does not show esc, the way out of the mode: %q", row)
	}
}

var hintsLegendItems = []legendItem{
	{key: "label", label: "copy"},
	{key: "shift+label", short: []string{"S-label"}, label: "type"},
	{key: "ctrl+label", short: []string{"^label"}, label: "open"},
	{key: "?", label: "help"},
	{key: "esc", label: "cancel"},
}

func startLegend(t *testing.T, size legendSize) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, t.TempDir(), startOpts{cols: size.cols, rows: size.rows, env: size.env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	return term
}

func waitLegendRow(t *testing.T, term *tuitest.Terminal, what string, ok func(string) bool) string {
	t.Helper()
	var row string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row = dockRow(s)
		return ok(row)
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %v\ndock row: %q\n%s", what, err, row, term.Snapshot())
	}
	return row
}

// TestHintsModeLegend opens hints mode at 60, 80 and 200 columns and reads
// the legend off the dock: no word cut, the copy key and esc always there,
// and every key at 200 columns. The old message is not on the screen at all.
func TestHintsModeLegend(t *testing.T) {
	for _, size := range legendSizes {
		t.Run(size.name(), func(t *testing.T) {
			term := startLegend(t, size)
			dir := artifactDir(t)
			runInShell(t, term, "echo https://example.com/legend ; echo LEGEND\"\"-READY", "LEGEND-READY", shellTimeout)
			if row := dockRow(term.Screen()); strings.Contains(row, "label copy") {
				t.Fatalf("the legend is on the dock before hints mode opened: %q", row)
			}

			openHints(t, term)
			row := waitLegendRow(t, term, "no hints legend on the dock", func(r string) bool {
				return strings.Contains(r, "label copy") && strings.Contains(r, "HINTS")
			})
			saveArtifact(t, term, dir, "hints-open")
			checkLegend(t, row, hintsLegendItems, []string{"label", "?", "esc"}, size.cols >= 80, size.cols >= 200)
			checkKeyBold(t, term.Screen(), "label", "copy")
			if size.cols <= 60 && legendShown(row, hintsLegendItems[1]) && legendShown(row, hintsLegendItems[2]) {
				t.Errorf("at %d columns the legend kept every key, so this width tests no fitting: %q", size.cols, row)
			}
			if strings.Contains(term.Snapshot(), "Type a label") {
				t.Errorf("hints mode still says its keys in a message:\n%s", term.Snapshot())
			}

			if err := term.SendKeys("\x1b"); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			waitLegendRow(t, term, "the legend stayed after esc closed hints mode", func(r string) bool {
				return !strings.Contains(r, "label copy") && !strings.Contains(r, "HINTS")
			})
			saveArtifact(t, term, dir, "hints-closed")
		})
	}
}

// TestHintsHelpKeyListsAllKeys presses ? in hints mode: hints mode closes and
// the help opens on the section that lists every hints key, the ones the
// legend had no room for among them.
func TestHintsHelpKeyListsAllKeys(t *testing.T) {
	term := startLegend(t, legendSize{cols: 80, rows: 24})
	runInShell(t, term, "echo https://example.com/legend ; echo LEGEND\"\"-READY", "LEGEND-READY", shellTimeout)
	openHints(t, term)
	waitLegendRow(t, term, "no hints legend on the dock", func(r string) bool { return strings.Contains(r, "label copy") })
	if err := term.SendKeys("?"); err != nil {
		t.Fatalf("send ?: %v", err)
	}
	if err := term.WaitForText("Hints: open the link", uiTimeout); err != nil {
		t.Fatalf("? did not open the help on hints mode's keys: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-help")
	if row := dockRow(term.Screen()); strings.Contains(row, "HINTS") {
		t.Errorf("hints mode is still open under the help: %q", row)
	}
}

var copyLegendItems = []legendItem{
	{key: "hjkl", label: "move"},
	{key: "w/b/e", label: "word"},
	{key: "f/t", label: "char"},
	{key: "/", label: "search"},
	{key: "n/N", label: "next"},
	{key: "v", label: "visual"},
	{key: "ctrl+v", label: "block"},
	{key: "y", label: "yank"},
	{key: "ctrl+g", label: "edit"},
	{key: "s", label: "hints"},
	{key: "q/enter/esc", short: []string{"q/↵/esc"}, label: "quit"},
	{key: "ctrl+c", short: []string{"^c"}, label: "quit"},
}

var copySearchLegendItems = []legendItem{
	{key: "type", label: "search"},
	{key: "n/N", label: "next"},
	{key: "enter", short: []string{"↵"}, label: "done"},
	{key: "esc", label: "cancel"},
	{key: "ctrl+c", short: []string{"^c"}, label: "quit"},
}

var copyVisualLegendItems = []legendItem{
	{key: "hjkl", label: "extend"},
	{key: "w/b/e", label: "word"},
	{key: "%", label: "bracket"},
	{key: "y", label: "yank"},
	{key: "enter", short: []string{"↵"}, label: "yank and quit"},
	{key: "esc", label: "cancel"},
	{key: "ctrl+c", short: []string{"^c"}, label: "quit"},
}

// TestCopyModeLegend enters copy mode at 60, 80 and 200 columns, then starts
// a selection, and reads each legend off the dock by the same rule.
func TestCopyModeLegend(t *testing.T) {
	for _, size := range legendSizes {
		t.Run(size.name(), func(t *testing.T) {
			term := startLegend(t, size)
			dir := artifactDir(t)
			runInShell(t, term, "echo LEGEND\"\"-READY", "LEGEND-READY", shellTimeout)
			if row := dockRow(term.Screen()); strings.Contains(row, "yank") {
				t.Fatalf("the legend is on the dock before copy mode opened: %q", row)
			}

			if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
				t.Fatalf("send leader [: %v", err)
			}
			row := waitLegendRow(t, term, "no copy mode legend on the dock", func(r string) bool {
				return strings.Contains(r, "y yank") && strings.Contains(r, "quit")
			})
			saveArtifact(t, term, dir, "copy-normal")
			checkLegend(t, row, copyLegendItems, []string{"y", "q/enter/esc"}, size.cols >= 80, size.cols >= 200)

			if err := term.SendKeys("v"); err != nil {
				t.Fatalf("send v: %v", err)
			}
			row = waitLegendRow(t, term, "no visual legend on the dock", func(r string) bool {
				return strings.Contains(r, "y yank") && strings.Contains(r, "cancel")
			})
			saveArtifact(t, term, dir, "copy-visual")
			checkLegend(t, row, copyVisualLegendItems, []string{"y", "esc"}, size.cols >= 80, size.cols >= 200)

			if err := term.SendKeys("\x1b"); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			waitLegendRow(t, term, "esc did not end the selection", func(r string) bool {
				return strings.Contains(r, "quit") && !strings.Contains(r, "cancel")
			})

			if err := term.SendKeys("/"); err != nil {
				t.Fatalf("send /: %v", err)
			}
			row = waitLegendRow(t, term, "no search legend on the dock", func(r string) bool {
				return strings.Contains(r, "done") && strings.Contains(r, "cancel")
			})
			saveArtifact(t, term, dir, "copy-search")
			checkLegend(t, row, copySearchLegendItems, []string{"enter"}, size.cols >= 80, size.cols >= 200)
			if err := term.SendKeys("\x1b"); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			waitLegendRow(t, term, "esc did not close the search prompt", func(r string) bool {
				return strings.Contains(r, "quit") && !strings.Contains(r, "cancel")
			})
			if err := term.SendKeys("q"); err != nil {
				t.Fatalf("send q: %v", err)
			}
			waitLegendRow(t, term, "the legend stayed after copy mode closed", func(r string) bool {
				return !strings.Contains(r, "yank")
			})
		})
	}
}

// TestCutMessageEndsOnAWholeWord raises a message too long for the dock, made
// of three-digit numbers, at four widths one column apart. A cut that ignores
// words falls inside a number at three of any four widths, so each width must
// show only whole numbers before the ellipsis, each the one after the last.
func TestCutMessageEndsOnAWholeWord(t *testing.T) {
	for cols := 80; cols < 84; cols++ {
		t.Run(fmt.Sprintf("%dx24", cols), func(t *testing.T) {
			term, _ := start(t, startOpts{cols: cols, rows: 24})
			waitBoot(t, term)
			newWindow(t, term)
			raiseLongMessage(t, term, 100, 220)
			row := dockRow(term.Screen())
			saveArtifact(t, term, artifactDir(t), "cut-message")

			i := strings.Index(row, longHead)
			j := strings.Index(row, "…")
			if j < 0 {
				j = strings.Index(row, "...")
			}
			if i < 0 || j < i {
				t.Fatalf("the dock does not show the cut message with an ellipsis: %q", row)
			}
			words := strings.Fields(row[i+len(longHead) : j])
			if len(words) < 2 {
				t.Fatalf("the cut message shows fewer than two numbers: %q", row)
			}
			for k, w := range words {
				if w != fmt.Sprint(100+k) {
					t.Errorf("word %d of the cut message is %q, want %d: %q", k, w, 100+k, row)
				}
			}
		})
	}
}
