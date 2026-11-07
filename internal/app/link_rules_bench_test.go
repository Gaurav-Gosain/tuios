package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// BenchmarkRenderTerminalLinkRules measures a focused pane frame at the real
// host size with no link rules and with one ticket rule, over a screen with no
// ticket ID on it. The rule has to look at every line, and the difference to
// "none" is what turning the feature on costs.
//
// "lower" is the usual benchmark screen, all lowercase: the ticket rule's
// first byte is A-Z, so every line is skipped before the regex runs. "upper"
// has capitals on every line, so every line runs the regex, which is the worst
// case for a rule like this one.
func BenchmarkRenderTerminalLinkRules(b *testing.B) {
	rules, errs := hints.CompileLinkRules([]hints.LinkRuleSpec{{
		Regex: `\b([A-Z][A-Z0-9_]+-[1-9][0-9]*)\b`,
		URL:   "https://jira.example.com/browse/$1",
	}})
	if len(errs) > 0 {
		b.Fatal(errs)
	}
	screens := []struct {
		name string
		fill func(testing.TB, *terminal.Window, int, int)
	}{
		{"lower", fillWindow},
		{"upper", fillWindowCapitals},
	}
	for _, screen := range screens {
		for _, c := range []struct {
			name  string
			rules *hints.LinkRules
		}{
			{"none", nil},
			{"one", rules},
		} {
			b.Run(screen.name+"/"+c.name, func(b *testing.B) {
				win := newTestWindow(b, "bench-rules-"+screen.name+c.name, realCols, realRows)
				screen.fill(b, win, realCols, realRows)
				m := newTestOS(win)
				m.Settings.Links = config.LinksAll
				m.Settings.LinkRules = c.rules
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					win.MarkContentDirty()
					_ = m.renderTerminal(win, true, true)
				}
			})
		}
	}
}

// fillWindowCapitals paints every row with log-like text that has capitals and
// hyphenated words but no ticket ID, so a ticket rule runs on every line and
// matches none.
func fillWindowCapitals(tb testing.TB, win *terminal.Window, cols, rows int) {
	tb.Helper()
	win.LockIO()
	defer win.UnlockIO()
	for y := 1; y <= rows; y++ {
		line := fmt.Sprintf("INFO %03d Request served by api-gateway ", y)
		for len(line) < cols-12 {
			line += "Status OK "
		}
		_, _ = win.Terminal.Write(fmt.Appendf(nil,
			"\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m", y, 16+(y%200), line))
	}
}
