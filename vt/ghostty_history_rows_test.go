//go:build ghostty

package vt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/vt"
	"tuios.dev/vt/vtgen"
)

// The ghostty backend reads the text of history rows for search through the
// formatter and decodes it in Go (ghostty_history_rows.go), and reads a row
// cell by cell only when a check says the decoded row may be wrong. These
// tests hold every row the fast path calls exact to the cell-by-cell read in
// text and width, so a row that does not line up is found here and not by a
// person.

// historyCompare writes data to a pane with history and compares its rows.
func historyCompare(t *testing.T, name string, cols, rows int, writes []string) (exact, fallback int) {
	t.Helper()
	term := vt.NewGhosttyTerminal(cols, rows)
	defer func() { _ = term.Close() }()
	for _, w := range writes {
		_, _ = term.Write([]byte(w))
	}
	// Push the screen into the history so every row written is compared.
	_, _ = term.Write([]byte("\x1b[0m" + strings.Repeat("\r\n", rows)))
	exact, fallback, mismatches := vt.CompareHistoryRows(term)
	for i, m := range mismatches {
		if i == 5 {
			t.Errorf("%s: %d more", name, len(mismatches)-5)
			break
		}
		t.Errorf("%s: %s", name, m)
	}
	return exact, fallback
}

func TestGhosttyHistoryRowsMatchCellReads(t *testing.T) {
	var exact, fallback int
	t.Run("vtgen", func(t *testing.T) {
		seeds := 300
		if testing.Short() {
			seeds = 60
		}
		if n, err := strconv.Atoi(os.Getenv("TUIOS_HISTORY_ROWS_SEEDS")); err == nil && n > 0 {
			seeds = n
		}
		for seed := range uint64(seeds) {
			script := vtgen.New(seed).Script(120)
			var writes []string
			cols, rows := 40, 12
			for _, seq := range script {
				if seq.Kind == "resize" {
					continue
				}
				writes = append(writes, seq.Bytes)
			}
			e, f := historyCompare(t, fmt.Sprintf("seed %d", seed), cols, rows, writes)
			exact, fallback = exact+e, fallback+f
		}
	})
	t.Run("graphemes", func(t *testing.T) {
		var b strings.Builder
		for _, tc := range loadGraphemeBreakTest(t) {
			if hasControl(tc.input) {
				continue
			}
			b.WriteString(tc.input)
			b.WriteString("\r\n")
		}
		e, f := historyCompare(t, "GraphemeBreakTest", 80, 24, []string{b.String()})
		exact, fallback = exact+e, fallback+f
	})
	t.Run("widths", func(t *testing.T) {
		var b strings.Builder
		n := 0
		for _, rg := range loadEastAsianWidth(t) {
			for r := rg.lo; r <= rg.hi; r++ {
				if !printableForWidth(r) {
					continue
				}
				b.WriteRune(r)
				if n++; n%30 == 0 {
					b.WriteString("\r\n")
				}
			}
		}
		e, f := historyCompare(t, "EastAsianWidth", 80, 24, []string{b.String()})
		exact, fallback = exact+e, fallback+f
	})
	t.Logf("rows read exact: %d, handed to the cell-by-cell read: %d", exact, fallback)
	if exact == 0 {
		t.Fatal("no row took the fast path, so nothing was compared")
	}
}

// TestGhosttyHistoryRowsFallbackRate reports how often the fallback fires on
// streams like real output: the stored corpus (git log, git diff, ls --color
// and others), and with VTBENCH_REC, recorded sessions, run on the main
// screen so their output reaches the history.
func TestGhosttyHistoryRowsFallbackRate(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "corpus", "*.bin"))
	if dir := os.Getenv("VTBENCH_REC"); dir != "" {
		rec, _ := filepath.Glob(filepath.Join(dir, "*.raw"))
		files = append(files, rec...)
	}
	sort.Strings(files)
	mainScreen := strings.NewReplacer("\x1b[?1049h", "", "\x1b[?1047h", "", "\x1b[?47h", "", "\x1b[2J", "\r\n")
	var exact, fallback int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Each stream three times over, so it leaves history behind.
		text := mainScreen.Replace(string(data))
		e, fb := historyCompare(t, filepath.Base(f), 207, 55, []string{text, "\r\n", text, "\r\n", text})
		exact, fallback = exact+e, fallback+fb
		t.Logf("FALLBACK %s: %d rows exact, %d to the cell-by-cell read (%.1f%%)", filepath.Base(f), e, fb,
			100*float64(fb)/float64(max(e+fb, 1)))
	}
	t.Logf("FALLBACK all: %d rows exact, %d to the cell-by-cell read (%.1f%%)", exact, fallback,
		100*float64(fallback)/float64(max(exact+fallback, 1)))
}
