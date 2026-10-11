package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railText is the text of the rail on one edge, w columns wide with its
// border, as one string a line. Read by column rather than up to the first
// border glyph, because a tree line on the rail is that glyph too.
func railText(s tuitest.Screen, w int, right bool) string {
	cols, rows := s.Size()
	out := make([]string, 0, rows)
	for y := range rows {
		line := []rune(s.Line(y))
		if right {
			// A line can come back without its trailing blanks.
			for len(line) < cols {
				line = append(line, ' ')
			}
			out = append(out, string(line[cols-w+1:]))
			continue
		}
		out = append(out, string(line[:min(w-1, len(line))]))
	}
	return strings.Join(out, "\n")
}

// subagentStylesRail is the rail both subagent style tests start from: the
// agents section alone, 24 columns, on the left.
const subagentStylesRail = "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 24\nsections = \"agents\"\n"

// startSubagentStyles attaches a 120x50 client to a session whose pane runs
// the stand-in claude, with config rail, and waits for its four subagents to
// run with a tool each.
func startSubagentStyles(t *testing.T, log *stateLog, rail string) (string, *tuitest.Terminal) {
	t.Helper()
	// Long enough that the finished rows outlive the early hide by far.
	t.Setenv("TUIOS_SUBAGENT_FADE_SECONDS", "60")
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, rail)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-sub", "--detach"); err != nil {
		t.Fatalf("create e2e-sub: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "e2e-sub", "-w", "0", "--name", "lead"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(base, "scenario.txt")
	if err := os.WriteFile(scenario, []byte(subagentRowsScenario()), 0o644); err != nil {
		t.Fatal(err)
	}
	hookLog := filepath.Join(base, "hooks.log")

	term := attachIn(t, base, "e2e-sub", startOpts{cols: 120, rows: 50})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", claude+" "+scenario+" "+hookLog+"\n"); err != nil {
		t.Fatalf("start the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT running")
	waitSubagents(t, base, "e2e-sub", log, "running", func(s subagentsOf) bool {
		return s.Subagents == 4 && s.byID(saCI).Now == "Bash: gh run list" && s.byID(saTmux).Now == "Bash: go test ./..."
	})
	return base, term
}

// TestSubagentRailStyles drives the stand-in claude of "Subagent rows" to four
// running subagents under a real client, and holds each subagent option of
// the rail to what it draws:
//
//   - the default draws the rows as before, each with a tall second line;
//   - subagent_rows = "tree" draws them on tree lines, ├─ to └─, with │ down
//     the second lines;
//   - subagent_collapse = "expanded" draws a heading, "▾ 4 subagents", over
//     the rows; a click on it shuts them (▸), its second line naming them,
//     and space on it opens them again;
//   - subagent_collapse = "collapsed" starts with the heading alone;
//   - subagent_fields = "mark,type" draws the agent type and no note line;
//   - subagent_max_rows = 2 draws one row and "+3 more";
//   - a right rail with its own subagent_rows = "tree" draws tree lines while
//     the left rail keeps the list;
//   - subagent_hide_finished = "2s" takes the two finished rows away while
//     the daemon still lists them.
//
// It saves the frame of each under TUIOS_E2E_FRAMES. Negative controls are in
// NEGATIVE_CONTROLS.md, "Subagent rail styles".
func TestSubagentRailStyles(t *testing.T) {
	log := &stateLog{name: "subagent-rail-styles"}
	defer log.save(t)
	const w, rows = 24, 50
	rail := subagentStylesRail
	base, term := startSubagentStyles(t, log, rail)

	// look rewrites the config and waits for the left rail to match.
	look := func(name, extra, why string, ok func(left string) bool) {
		t.Helper()
		writeConfig(t, base, rail+extra)
		var left string
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			left = railText(s, w, false)
			return ok(left)
		}, uiTimeout); err != nil {
			t.Fatalf("%s: %v\n%s", why, err, term.Snapshot())
		}
		log.add("%s\n%s", name, left)
		saveFrame(t, term, "subagent-style-"+name)
	}

	look("rows", "", "the default rail does not draw a tall row per subagent", func(l string) bool {
		return strings.Contains(l, "    ● Research") && strings.Contains(l, "      Bash: go test") &&
			strings.Contains(l, "    ● Check CI") && !strings.ContainsAny(l, "├└▾▸")
	})
	look("tree", "subagent_rows = \"tree\"\n", "subagent_rows = tree does not draw tree lines", func(l string) bool {
		return strings.Contains(l, " ├─ ● Research") && strings.Contains(l, " │    Bash: go test") &&
			strings.Contains(l, " └─ ● Check CI")
	})
	look("expanded", "subagent_collapse = \"expanded\"\n", "subagent_collapse = expanded draws no heading over the rows", func(l string) bool {
		return strings.Contains(l, "    ▾ 4 subagents") && strings.Contains(l, "    ● Research")
	})

	// A click on the heading shuts the rows. The rail takes the keyboard
	// first, so the click also puts the cursor on the heading, and space
	// there opens them again.
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("focus the rail: %v", err)
	}
	row, col := findText(t, term, "4 subagents")
	clickAt(t, term, col, row, 1)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		l := railText(s, w, false)
		return strings.Contains(l, "    ▸ 4 subagents") && strings.Contains(l, "      Research tmux, A") &&
			!strings.Contains(l, "● Research")
	}, uiTimeout); err != nil {
		t.Fatalf("a click on the heading did not shut the rows: %v\n%s", err, term.Snapshot())
	}
	log.add("shut by a click\n%s", railText(term.Screen(), w, false))
	saveFrame(t, term, "subagent-style-shut-by-click")
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("press space: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		l := railText(s, w, false)
		return strings.Contains(l, "▾ 4 subagents") && strings.Contains(l, "● Research")
	}, uiTimeout); err != nil {
		t.Fatalf("space on the heading did not open the rows: %v\n%s", err, term.Snapshot())
	}
	log.add("opened by space\n%s", railText(term.Screen(), w, false))
	saveFrame(t, term, "subagent-style-opened-by-space")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("leave the rail: %v", err)
	}

	// The pane was opened by hand, so a new start state applies to it only
	// once the client forgets that. The collapsed start is read from a fresh
	// client.
	writeConfig(t, base, rail+"subagent_collapse = \"collapsed\"\n")
	fresh := attachIn(t, base, "e2e-sub", startOpts{cols: 120, rows: rows})
	if err := fresh.WaitFor(func(s tuitest.Screen) bool {
		l := railText(s, w, false)
		return strings.Contains(l, "    ▸ 4 subagents") && !strings.Contains(l, "● Research")
	}, bootTimeout); err != nil {
		t.Fatalf("subagent_collapse = collapsed does not start with the heading alone: %v\n%s", err, fresh.Snapshot())
	}
	log.add("collapsed\n%s", railText(fresh.Screen(), w, false))
	saveFrame(t, fresh, "subagent-style-collapsed")
	alive(t, fresh, "after the collapsed start")
	fresh.Close()

	look("fields", "subagent_fields = \"mark,type\"\n", "subagent_fields = mark,type does not draw the type alone", func(l string) bool {
		return strings.Contains(l, "    ● general-purpose") && strings.Contains(l, "    ● Explore") &&
			!strings.Contains(l, "Research") && !strings.Contains(l, "Bash: go test")
	})
	look("max-rows", "subagent_max_rows = 2\n", "subagent_max_rows = 2 does not draw one row and +3 more", func(l string) bool {
		return strings.Contains(l, "● Check CI") && strings.Contains(l, "+3 more") &&
			!strings.Contains(l, "Research") && !strings.Contains(l, "Audit")
	})

	// A second rail on the right edge with its own style: tree lines there,
	// the list on the left.
	edge := "[appearance.sidebar.right]\nenabled = true\nwidth = 24\nsections = \"agents\"\nsubagent_rows = \"tree\"\n"
	writeConfig(t, base, rail+edge)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		l, r := railText(s, w, false), railText(s, w, true)
		return strings.Contains(l, "    ● Research") && !strings.Contains(l, "├─") &&
			strings.Contains(r, " ├─ ● Research") && strings.Contains(r, " └─ ● Check CI")
	}, uiTimeout); err != nil {
		t.Fatalf("the right rail does not draw its own subagent style: %v\n%s", err, term.Snapshot())
	}
	log.add("per edge\nleft:\n%s\nright:\n%s", railText(term.Screen(), w, false), railText(term.Screen(), w, true))
	saveFrame(t, term, "subagent-style-per-edge")

	// Two end. The early hide takes their rows away while the daemon still
	// lists all four.
	writeConfig(t, base, rail+"subagent_hide_finished = \"2s\"\n")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
		t.Fatalf("step the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT two-ended")
	ended := time.Now()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		l := railText(s, w, false)
		return strings.Contains(l, "● Audit") && strings.Contains(l, "● Check CI") &&
			!strings.Contains(l, "Research") && !strings.Contains(l, "Write the")
	}, 20*time.Second); err != nil {
		t.Fatalf("subagent_hide_finished = 2s did not take the finished rows away: %v\n%s", err, term.Snapshot())
	}
	if n := len(readSubagents(t, base, "e2e-sub").List); n != 4 {
		t.Fatalf("the daemon lists %d subagents, want 4: the rows went by the daemon's fade, not the option", n)
	}
	log.add("hidden %.1fs after the end\n%s", time.Since(ended).Seconds(), railText(term.Screen(), w, false))
	saveFrame(t, term, "subagent-style-hide-finished")
	writeConfig(t, base, rail)
	alive(t, term, "after every subagent style")
}

// TestSubagentHideTimer holds subagent_hide_finished to its own frame. The
// option is set before anything ends and nothing is sent to the client after
// the start: no key, no pointer, no config write. Two subagents end, the
// pane goes quiet, and the daemon keeps listing them for 60 seconds, so the
// only thing that can take their rows away 8 seconds later is the frame the
// client arms for that moment (subagentHideCmd). The rows must still be
// there 4 seconds after the end, and gone by 14.
//
// Negative control in NEGATIVE_CONTROLS.md, "Subagent rail styles".
func TestSubagentHideTimer(t *testing.T) {
	log := &stateLog{name: "subagent-hide-timer"}
	defer log.save(t)
	const w = 24
	base, term := startSubagentStyles(t, log, subagentStylesRail+"subagent_hide_finished = \"8s\"\n")
	finished := func(l string) bool { return strings.Contains(l, "Research") && strings.Contains(l, "Write the") }
	if err := term.WaitFor(func(s tuitest.Screen) bool { return finished(railText(s, w, false)) }, uiTimeout); err != nil {
		t.Fatalf("the rail never drew the four rows: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
		t.Fatalf("step the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT two-ended")
	ended := time.Now()
	// The rows of the two that ended stay, muted, until the hide is due.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return time.Since(ended) >= 4*time.Second
	}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if l := railText(term.Screen(), w, false); !finished(l) {
		t.Fatalf("the finished rows went %.1fs after the end, before the 8s hide:\n%s", time.Since(ended).Seconds(), term.Snapshot())
	}
	saveFrame(t, term, "subagent-hide-timer-before")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		l := railText(s, w, false)
		return !strings.Contains(l, "Research") && !strings.Contains(l, "Write the") && strings.Contains(l, "● Audit")
	}, 10*time.Second); err != nil {
		t.Fatalf("subagent_hide_finished = 8s did not take the finished rows away by %.1fs after the end: %v\n%s",
			time.Since(ended).Seconds(), err, term.Snapshot())
	}
	took := time.Since(ended)
	if n := len(readSubagents(t, base, "e2e-sub").List); n != 4 {
		t.Fatalf("the daemon lists %d subagents, want 4: the rows went by the daemon's fade, not the option", n)
	}
	log.add("hidden %.1fs after the end\n%s", took.Seconds(), railText(term.Screen(), w, false))
	saveFrame(t, term, "subagent-hide-timer-after")
	alive(t, term, "after the hide timer")
}
