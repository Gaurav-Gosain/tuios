package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestComposerLookAtEveryDepth opens the reply editor in the agents view at
// 16, 256 and 24-bit colour, types a draft and double clicks a word of it,
// and checks the look the person asked for:
//
//   - one ground: the draft is drawn on the reply area's own ground, with no
//     lighter band inside it;
//   - a focus edge: while the editor has the keys, a bar runs down the left
//     of the reply area, on every row of it;
//   - the text column: the header, the draft's first character and the hint
//     line start in the same column;
//   - the selection shows at every depth, by reverse video where no ground
//     can, and the cursor beside it stays a cursor of its own: underlined
//     at 16 colours, where a second reversed cell would join the selection.
//
// Frames of each depth are saved under artifactDir.
//
// How it could pass wrongly, written down first:
//   - The ground check could compare two cells both off the draft. It
//     compares the cell right after the draft's text, on the draft's row,
//     with the header row's cell in the same column.
//   - The bar could be drawn on the header row only. The check reads it on
//     the header, the draft and the hint rows.
//   - At 16 colours every ground is the terminal's own, so a selection drawn
//     only with a ground would pass a ground check and show nothing. The
//     check there wants the selected cells reversed.
//   - The column check could find the header's text where the hints are.
//     It finds each by its own words, on its own row.
func TestComposerLookAtEveryDepth(t *testing.T) {
	depths := []struct {
		name string
		env  []string
	}{
		{"16", []string{"TERM=xterm", "COLORTERM="}},
		{"256", []string{"TERM=xterm-256color", "COLORTERM="}},
		{"truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
	}
	for _, d := range depths {
		t.Run(d.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			for _, s := range []string{"home", "docs"} {
				if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
					t.Fatalf("create session %s: %v\n%s", s, err, out)
				}
			}
			for _, args := range [][]string{
				{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
				{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
			} {
				if out, err := tuiosCLI(t, base, args...); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
			}
			term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 32, shippedLooks: true,
				env: append([]string{"TUIOS_KITTY_GRAPHICS=0"}, d.env...)})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatal(err)
			}
			waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.")
			_ = term.WaitStable(uiTimeout)
			saveCockpitFrames(t, term, "composer-look-closed-"+d.name)
			row, col := findText(t, term, "Write a reply.")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			waitText(t, term, "the open reply editor", "Type your reply.")
			if err := term.SendKeys("alpha beta gamma"); err != nil {
				t.Fatal(err)
			}
			waitText(t, term, "the draft", "alpha beta gamma")
			drow, dcol := findText(t, term, "alpha beta gamma")
			mouseClick(t, term, dcol+13, drow, tuitest.MouseLeft, 0)
			mouseClick(t, term, dcol+13, drow, tuitest.MouseLeft, 0)
			_ = term.WaitStable(uiTimeout)
			saveCockpitFrames(t, term, "composer-look-open-"+d.name)

			s := term.Screen()
			hrow, hcol := findText(t, term, "Reply to docs-cli")
			mrow, mcol := findText(t, term, "@ mention")
			if hcol != dcol || mcol != dcol {
				t.Errorf("ASSERTION: the header (col %d), the draft (col %d) and the hints (col %d) do not share a column\n%s", hcol, dcol, mcol, term.Snapshot())
			}
			after := dcol + len("alpha beta gamma") + 3
			if got, want := s.Cell(after, drow).Bg, s.Cell(after, hrow).Bg; got != want {
				t.Errorf("ASSERTION: the draft's row has its own ground %+v, the reply area's is %+v\n%s", got, want, term.Snapshot())
			}
			// The bar is the first cell of the area, two left of the
			// sigil, which is two left of the text.
			for _, r := range []int{hrow, drow, mrow} {
				if c := s.Cell(dcol-4, r).Content; !strings.ContainsAny(c, "▌▎|") {
					t.Errorf("ASSERTION: no focus edge on row %d: %q\n%s", r, c, term.Snapshot())
				}
			}
			if d.name == "16" {
				for i := range len("gamma") {
					if c := s.Cell(dcol+11+i, drow); !c.Reverse {
						t.Errorf("ASSERTION: selected cell %d of gamma is not reversed at 16 colours\n%s", i, term.Snapshot())
					}
				}
				// The cursor right after the selection is not reversed
				// too, or the two read as one bar.
				if c := s.Cell(dcol+16, drow); c.Reverse || !c.Underline {
					t.Errorf("ASSERTION: the cursor beside the selection at 16 colours is not underlined (reverse %v, underline %v)\n%s", c.Reverse, c.Underline, term.Snapshot())
				}
			}
		})
	}
}
