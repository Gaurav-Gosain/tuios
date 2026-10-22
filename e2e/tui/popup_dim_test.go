package tuie2e

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The option and overrides must survive gob; pane, dock and rail dim while
// the popup stays lit. Focusing away and back must remove and restore the dim.
func TestPopupDimsTheScreenBehind(t *testing.T) {
	for _, look := range chromeLooks {
		for _, depth := range chromeDepths {
			t.Run(look.name+"-"+depth.name, func(t *testing.T) {
				popupDimRun(t, look.theme, depth)
			})
		}
	}
}

func popupDimRun(t *testing.T, themeName string, depth chromeDepth) {
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, fmt.Sprintf(`
[startup]
open_default_window = true
tiled = true
[appearance]
theme = %q
dim_unfocused = 0
modal_dim = 0
show_clock = true
dockbar_position = "bottom"
[dock]
left = ["mode", "session", "workspaces"]
center = ["clock"]
right = ["notifications", "session-controls"]
[dock.clock]
format = "DIMDOCK"
`, themeName))
	term := startIn(t, base, startOpts{args: []string{"new", "e2e-dim"}, shippedLooks: true, env: depth.env})
	waitWindowCount(t, term, 1, "the coloured pane")
	pane := waitForSettledGeometryIn(t, base, "e2e-dim", 1)[0].ID
	enterTerminalMode(t, term)
	runInShell(t, term, dimCommand, "DIMANSIx", shellTimeout)
	windowManagementMode(t, term)
	if err := term.WaitForText("DIMDOCK", uiTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	before := term.Screen()
	words := findDimWords(t, term, before)
	dockRow, dockCol, _ := textAt(before, "DIMDOCK", 0)
	railRow, railCol := railNameCell(t, term, before)
	points := append(words, [2]int{dockCol, dockRow}, [2]int{railCol, railRow})
	plain := make([]tuitest.Cell, len(points))
	for i, at := range points {
		plain[i] = before.Cell(at[0], at[1])
	}
	dir, host := artifactDir(t), hostPalette(t, themeName)
	var own []tuitest.Cell
	read := func(label string, dim bool) []tuitest.Cell {
		t.Helper()
		if err := term.WaitForText("POPUPLITx", uiTimeout); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			for i, at := range points {
				if dim == (s.Cell(at[0], at[1]) == plain[i]) {
					return false
				}
			}
			return true
		}, uiTimeout); err != nil {
			t.Fatalf("%s: pane, rail or dock cells did not reach dim=%v: %v\n%s", label, dim, err, term.SnapshotStyled())
		}
		s := term.Screen()
		cells := make([]tuitest.Cell, len(points))
		for i, at := range points {
			cells[i] = s.Cell(at[0], at[1])
			if dim && i < len(dimWords) {
				checkDimmed(t, term, dimWords[i], plain[i], cells[i], depth.name, themeName)
			}
		}
		row, col, ok := textAt(s, "POPUPLITx", 0)
		if !ok {
			t.Fatal("the popup disappeared")
		}
		for x := range len("POPUPLITx") {
			c := s.Cell(col+x, row)
			if own != nil && c != own[x] {
				t.Fatalf("%s: popup cell %d changed: %+v then %+v", label, x, own[x], c)
			}
		}
		if own == nil {
			for x := range len("POPUPLITx") {
				own = append(own, s.Cell(col+x, row))
			}
		}
		saveArtifact(t, term, dir, label)
		savePNG(t, s, host, dir, label)
		t.Logf("%s: dim=%v; popup stays lit", label, dim)
		return cells
	}
	var option30 []tuitest.Cell
	for _, step := range []struct {
		name   string
		option int
		flag   []string
		dim    bool
	}{
		{"default-off", 0, nil, false},
		{"option-30", 30, nil, true},
		{"flag-60", 30, []string{"--dim", "60"}, true},
		{"flag-zero", 30, []string{"--dim", "0"}, false},
		{"option-off", 0, nil, false},
	} {
		if step.name != "default-off" {
			if out, err := tuiosCLI(t, base, "set-config", "appearance.popup_dim", fmt.Sprint(step.option)); err != nil {
				t.Fatalf("set dim: %v\n%s", err, out)
			}
		}
		args := append([]string{"--width", "40%", "--height", "30%"}, step.flag...)
		popup := openPopup(t, base, append(args, "--", "sh", "-c", `printf '\033[38;2;200;150;90m\033[48;2;48;52;70mPOPUPLIT%s\033[m\n' x; exec cat`)...)
		cells := read(step.name, step.dim)
		if step.name == "option-30" {
			option30 = cells
		}
		if step.name == "flag-60" {
			if depth.name != "16" && cells[0].Fg == option30[0].Fg {
				t.Fatal("--dim 60 kept the option's 30 percent")
			}
			for _, focus := range []struct {
				id, label string
				dim       bool
			}{{pane, "focus-away", false}, {popup.WindowID, "focus-back", true}} {
				if out, err := tuiosCLI(t, base, "focus-window", focus.id); err != nil {
					t.Fatalf("focus: %v\n%s", err, out)
				}
				read(focus.label, focus.dim)
			}
		}
		if out, err := tuiosCLI(t, base, "close-window", popup.WindowID); err != nil {
			t.Fatalf("close popup: %v\n%s", err, out)
		}
		waitWindowCount(t, term, 1, "after closing the popup")
	}
}
