package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// defaultWindowModeIcon is config.DockModeIconWindow, nf-fa-window_restore,
// spelled out because this module does not import the one under test.
const defaultWindowModeIcon = ""

// dockStatusRow is the row that carries the dock's "1:" readout, or -1.
func dockStatusRow(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), "1:") {
			return r
		}
	}
	return -1
}

// cellColumnOf is the screen column where text starts on row, counting a wide
// cell as the two columns it takes, or -1 when the row does not carry it.
func cellColumnOf(s tuitest.Screen, row int, text string) int {
	cols, _ := s.Size()
	var line strings.Builder
	var starts []int
	for c := range cols {
		cell := s.Cell(c, row)
		if cell.Width == 0 {
			continue
		}
		content := cell.Content
		if content == "" {
			content = " "
		}
		for range content {
			starts = append(starts, c)
		}
		line.WriteString(content)
	}
	idx := strings.Index(line.String(), text)
	if idx < 0 {
		return -1
	}
	return starts[len([]rune(line.String()[:idx]))]
}

// pillFill is the run of columns on row that share the background of the cell
// at col: the mode pill's filled body when col is inside its icon. The
// continuation column of a wide cell carries no style of its own, so it counts
// as part of the run it follows.
func pillFill(s tuitest.Screen, row, col int) int {
	bg := s.Cell(col, row).Bg
	if bg.Kind == tuitest.ColorDefault {
		return 0
	}
	in := func(c int) bool {
		cell := s.Cell(c, row)
		return cell.Bg == bg || (cell.Width == 0 && c > 0 && s.Cell(c-1, row).Bg == bg)
	}
	left, right := col, col
	for left > 0 && in(left-1) {
		left--
	}
	cols, _ := s.Size()
	for right+1 < cols && in(right+1) {
		right++
	}
	return right - left + 1
}

// startDockModeIcons starts tuios with the [appearance] body and waits for a
// whole dock: one that shows the workspace readout.
func startDockModeIcons(t *testing.T, appearance string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\n"+appearance)
	term := startIn(t, base, startOpts{cols: 120, rows: 30})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := dockStatusRow(s)
		return r >= 0 && strings.Contains(s.Line(r), "1:0")
	}, bootTimeout); err != nil {
		t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	return term
}

// TestDockModeIconsFollowTheConfig: appearance.dock_mode_icon_window,
// _terminal and _tiling set the dock's mode pill icon, an empty value shows
// no pill, and a value the dock cannot lay out falls back to the default. A
// wide icon takes the cells it is wide: the pill's filled body grows by the
// icon's width, and the workspace readout after it moves right by the same
// amount, so nothing after the pill overlaps it.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: cutting the three
// assignments in ApplyAppearanceConfig fails custom, terminal, hidden and
// compact. Drawing the empty label as a pill fails hidden. Dropping the width
// limit in DockModeIconUsable fails too-wide.
func TestDockModeIconsFollowTheConfig(t *testing.T) {
	const wide = "終端" // two wide characters, four cells

	base := startDockModeIcons(t, "")
	bs := base.Screen()
	baseRow := dockStatusRow(bs)
	baseIcon := cellColumnOf(bs, baseRow, defaultWindowModeIcon)
	baseStatus := cellColumnOf(bs, baseRow, "1:0")
	baseCaps := len(dockCapsIn(bs.Line(baseRow)))
	if baseIcon < 0 {
		t.Fatalf("default: the dock row shows no window mode icon\n%s", bs.Line(baseRow))
	}
	// The positive half of every case below: unset draws the built-in icon in
	// a filled pill of one space, the icon and one space.
	if got := pillFill(bs, baseRow, baseIcon); got != 3 {
		t.Fatalf("default: the mode pill fill is %d cells, want 3\n%s", got, bs.Line(baseRow))
	}
	saveArtifact(t, base, artifactDir(t), "default")

	t.Run("custom", func(t *testing.T) {
		for _, look := range []struct{ name, theme string }{
			{"dark", ""},
			{"light", "catppuccin_latte"},
		} {
			cfg := "dock_mode_icon_window = \"" + wide + "\"\n" +
				"dock_mode_icon_tiling = \"TL\"\n"
			if look.theme != "" {
				cfg += "theme = \"" + look.theme + "\"\n"
			}
			term := startDockModeIcons(t, cfg)
			s := term.Screen()
			row := dockStatusRow(s)
			line := s.Line(row)
			icon := cellColumnOf(s, row, wide)
			if icon < 0 || strings.Contains(line, defaultWindowModeIcon) {
				t.Fatalf("%s: the dock row does not show the configured icon %q in place of the default\n%s", look.name, wide, line)
			}
			if got := pillFill(s, row, icon); got != 2+4 {
				t.Fatalf("%s: the mode pill fill is %d cells, want 6 for a four-cell icon\n%s", look.name, got, line)
			}
			if got, want := cellColumnOf(s, row, "1:0"), baseStatus+3; got != want {
				t.Fatalf("%s: the workspace readout is at column %d, want %d: three cells right of the default\n%s", look.name, got, want, line)
			}
			saveArtifact(t, term, artifactDir(t), look.name+"-window")

			// Tiling has its own icon, with the next split after it.
			if err := term.SendKeys("t"); err != nil {
				t.Fatalf("toggle tiling: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r := dockStatusRow(s)
				return r >= 0 && strings.Contains(s.Line(r), "TL") && !strings.Contains(s.Line(r), wide)
			}, uiTimeout); err != nil {
				t.Fatalf("%s: tiling never showed the configured icon TL: %v\n%s", look.name, err, term.Snapshot())
			}
			saveArtifact(t, term, artifactDir(t), look.name+"-tiling")
		}
	})

	t.Run("terminal", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_terminal = \">_\"\n")
		newWindow(t, term)
		enterTerminalMode(t, term)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			r := dockStatusRow(s)
			return r >= 0 && strings.Contains(s.Line(r), " >_ ")
		}, uiTimeout); err != nil {
			t.Fatalf("terminal mode never showed the configured icon >_: %v\n%s", err, term.Snapshot())
		}
		saveArtifact(t, term, artifactDir(t), "terminal")
	})

	t.Run("hidden", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_window = \"\"\n")
		s := term.Screen()
		row := dockStatusRow(s)
		line := s.Line(row)
		if strings.Contains(line, defaultWindowModeIcon) {
			t.Fatalf("the dock row shows the default icon with the icon set empty\n%s", line)
		}
		// The workspace pills keep their caps. The mode pill's two go.
		if caps := dockCapsIn(line); len(caps) != baseCaps-2 {
			t.Fatalf("the dock row draws %d pill caps, want %d: the default row's less the mode pill's two\n%s", len(caps), baseCaps-2, line)
		}
		if got := cellColumnOf(s, row, "1:0"); got < 0 || got >= baseStatus {
			t.Fatalf("the workspace readout is at column %d, want left of %d where the pill used to end\n%s", got, baseStatus, line)
		}
		saveArtifact(t, term, artifactDir(t), "hidden")
	})

	t.Run("too-wide", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_window = \"123456789\"\n")
		s := term.Screen()
		line := s.Line(dockStatusRow(s))
		if strings.Contains(line, "123456789") || !strings.Contains(line, defaultWindowModeIcon) {
			t.Fatalf("a nine-cell icon must fall back to the default icon\n%s", line)
		}
	})

	// The settings page lists the three rows: an empty icon reads as no icon,
	// and an unset one as the word that puts it back.
	t.Run("settings", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_window = \"\"\ndock_mode_icon_terminal = \">_\"\n")
		if err := term.SendKeys(",", "/", "mode icon"); err != nil {
			t.Fatalf("search the settings page: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			return strings.Contains(text, "Window mode icon") && strings.Contains(text, "(no icon)") &&
				strings.Contains(text, "Terminal mode icon") && strings.Contains(text, ">_") &&
				strings.Contains(text, "Tiling mode icon") && strings.Contains(text, "default")
		}, uiTimeout); err != nil {
			t.Fatalf("the settings page never listed the three mode icon rows: %v\n%s", err, term.Snapshot())
		}
		saveArtifact(t, term, artifactDir(t), "settings")
	})

	t.Run("compact", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_compact = true\ndock_mode_icon_window = \""+wide+"\"\n")
		s := term.Screen()
		row := dockStatusRow(s)
		line := s.Line(row)
		icon := cellColumnOf(s, row, wide)
		if icon < 0 {
			t.Fatalf("the compact dock does not show the configured icon\n%s", line)
		}
		if got := pillFill(s, row, icon); got != 6 {
			t.Fatalf("the compact mode pill fill is %d cells, want 6\n%s", got, line)
		}
		if status := cellColumnOf(s, row, "1:0"); status <= icon+4 {
			t.Fatalf("the workspace readout at column %d overlaps the icon at %d\n%s", status, icon, line)
		}
		saveArtifact(t, term, artifactDir(t), "compact")
	})
}
