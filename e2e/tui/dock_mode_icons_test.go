package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const defaultWindowModeIcon = "\uf2d2"

const prefixPillIcon = "\uf11c"

func dockStatusRow(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), "1:") {
			return r
		}
	}
	return -1
}

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
	const wide = "終端"

	base := startDockModeIcons(t, "")
	bs := base.Screen()
	baseRow := dockStatusRow(bs)
	baseIcon := cellColumnOf(bs, baseRow, defaultWindowModeIcon)
	baseStatus := cellColumnOf(bs, baseRow, "1:0")
	baseCaps := len(dockCapsIn(bs.Line(baseRow)))
	if baseIcon < 0 {
		t.Fatalf("default: the dock row shows no window mode icon\n%s", bs.Line(baseRow))
	}
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
		if caps := dockCapsIn(line); len(caps) != baseCaps-2 {
			t.Fatalf("the dock row draws %d pill caps, want %d: the default row's less the mode pill's two\n%s", len(caps), baseCaps-2, line)
		}
		if got := cellColumnOf(s, row, "1:0"); got < 0 || got >= baseStatus {
			t.Fatalf("the workspace readout is at column %d, want left of %d where the pill used to end\n%s", got, baseStatus, line)
		}
		saveArtifact(t, term, artifactDir(t), "hidden")
	})

	t.Run("prefix", func(t *testing.T) {
		for _, tc := range []struct{ name, cfg string }{
			{"pill", ""},
			{"badge", "dock_prefix_indicator = \"badge\"\n"},
		} {
			term := startDockModeIcons(t, tc.cfg)
			idle := term.Screen()
			idleRow := dockStatusRow(idle)
			idleBg := idle.Cell(cellColumnOf(idle, idleRow, defaultWindowModeIcon), idleRow).Bg
			if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
				t.Fatalf("%s: press the leader: %v", tc.name, err)
			}
			pill := tc.name == "pill"
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r := dockStatusRow(s)
				if r < 0 {
					return false
				}
				onPill := strings.Contains(s.Line(r), prefixPillIcon)
				badge := strings.Contains(s.Text(), "PREFIX")
				return onPill == pill && badge == !pill
			}, uiTimeout); err != nil {
				t.Fatalf("%s: the armed leader never showed where asked: %v\n%s", tc.name, err, term.Snapshot())
			}
			s := term.Screen()
			row := dockStatusRow(s)
			if pill {
				col := cellColumnOf(s, row, prefixPillIcon)
				if got := pillFill(s, row, col); got != 3 {
					t.Fatalf("pill: the armed pill fill is %d cells, want 3\n%s", got, s.Line(row))
				}
				if bg := s.Cell(col, row).Bg; bg == idleBg {
					t.Fatalf("pill: the armed pill keeps the window mode ground %+v\n%s", bg, term.SnapshotStyled())
				}
			}
			saveArtifact(t, term, artifactDir(t), "prefix-"+tc.name)
			if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
				t.Fatalf("%s: cancel the leader: %v", tc.name, err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r := dockStatusRow(s)
				return r >= 0 && strings.Contains(s.Line(r), defaultWindowModeIcon) && !strings.Contains(s.Text(), "PREFIX")
			}, uiTimeout); err != nil {
				t.Fatalf("%s: the pill never went back to window mode: %v\n%s", tc.name, err, term.Snapshot())
			}
		}
	})

	t.Run("split-letter", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			cfg    string
			letter bool
		}{
			{"default", "", true},
			{"off", "dock_mode_split_letter = false\n", false},
		} {
			term := startDockModeIcons(t, tc.cfg)
			if err := term.SendKeys("t"); err != nil {
				t.Fatalf("%s: toggle tiling: %v", tc.name, err)
			}
			if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
				t.Fatalf("%s: tiling never turned on: %v\n%s", tc.name, err, term.Snapshot())
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("%s: the screen never settled: %v", tc.name, err)
			}
			s := term.Screen()
			row := dockStatusRow(s)
			line := s.Line(row)
			tiled := cellColumnOf(s, row, "1:0")
			pill := line[:strings.Index(line, "1:0")]
			if got := strings.Contains(pill, " V ") || strings.Contains(pill, " H "); got != tc.letter {
				t.Fatalf("%s: the mode pill shows a split letter: %v, want %v\n%s", tc.name, got, tc.letter, line)
			}
			saveArtifact(t, term, artifactDir(t), "split-letter-"+tc.name)
			savePNG(t, s, hostPalette(t, ""), artifactDir(t), "split-letter-"+tc.name)

			if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
				t.Fatalf("%s: press the leader: %v", tc.name, err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r := dockStatusRow(s)
				return r >= 0 && strings.Contains(s.Line(r), prefixPillIcon)
			}, uiTimeout); err != nil {
				t.Fatalf("%s: the armed leader never showed on the pill: %v\n%s", tc.name, err, term.Snapshot())
			}
			armed := term.Screen()
			armedCol := cellColumnOf(armed, dockStatusRow(armed), "1:0")
			savePNG(t, armed, hostPalette(t, ""), artifactDir(t), "split-letter-"+tc.name+"-leader")
			if moved := armedCol != tiled; moved != tc.letter {
				t.Fatalf("%s: the readout moved from column %d to %d when the leader was pressed, want a move: %v\n%s",
					tc.name, tiled, armedCol, tc.letter, armed.Line(dockStatusRow(armed)))
			}
			if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
				t.Fatalf("%s: cancel the leader: %v", tc.name, err)
			}
		}
	})

	t.Run("mode-pill", func(t *testing.T) {
		icons := "dock_mode_icon_window = \"WM\"\ndock_mode_icon_terminal = \">_\"\ndock_mode_icon_tiling = \"TL\"\n"
		for _, tc := range []struct{ name, cfg, window, terminal string }{
			{"tiling", "", "TL", "TL"},
			{"input", "dock_mode_pill = \"input\"\n", "WM", ">_"},
		} {
			term := startDockModeIcons(t, icons+tc.cfg)
			if err := term.SendKeys("t"); err != nil {
				t.Fatalf("%s: toggle tiling: %v", tc.name, err)
			}
			newWindow(t, term)
			for _, step := range []struct{ mode, icon string }{
				{"window", tc.window},
				{"terminal", tc.terminal},
			} {
				if step.mode == "terminal" {
					enterTerminalMode(t, term)
				}
				want := []string{" " + step.icon + " V ", " " + step.icon + " H "}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					r := dockStatusRow(s)
					if r < 0 {
						return false
					}
					line := s.Line(r)
					pill := line[:max(0, strings.Index(line, "1:"))]
					return strings.Contains(pill, want[0]) || strings.Contains(pill, want[1])
				}, uiTimeout); err != nil {
					t.Fatalf("%s: %s mode never showed %q and the split letter on the pill: %v\n%s",
						tc.name, step.mode, step.icon, err, term.Snapshot())
				}
				s := term.Screen()
				saveArtifact(t, term, artifactDir(t), "mode-pill-"+tc.name+"-"+step.mode)
				savePNG(t, s, hostPalette(t, ""), artifactDir(t), "mode-pill-"+tc.name+"-"+step.mode)
			}
		}
	})

	t.Run("too-wide", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_window = \"123456789\"\n")
		s := term.Screen()
		line := s.Line(dockStatusRow(s))
		if strings.Contains(line, "123456789") || !strings.Contains(line, defaultWindowModeIcon) {
			t.Fatalf("a nine-cell icon must fall back to the default icon\n%s", line)
		}
	})

	t.Run("default-word", func(t *testing.T) {
		term := startDockModeIcons(t, "dock_mode_icon_window = \"default\"\n")
		s := term.Screen()
		line := s.Line(dockStatusRow(s))
		if strings.Contains(line, "default") || !strings.Contains(line, defaultWindowModeIcon) {
			t.Fatalf("the word default must draw the default icon\n%s", line)
		}
	})

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
