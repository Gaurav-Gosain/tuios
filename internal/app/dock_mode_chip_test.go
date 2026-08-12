package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sgrSetsBackground reports whether an SGR parameter list sets a background
// colour. 38 and 48 carry their own arguments, so the list is walked rather
// than searched: a foreground's blue component can otherwise read as a
// background code.
func sgrSetsBackground(params string) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "48":
			return true
		case "38":
			// 38;5;n (indexed) or 38;2;r;g;b (truecolor).
			if i+1 < len(fields) && fields[i+1] == "5" {
				i += 2
			} else {
				i += 4
			}
		default:
			if n, err := strconv.Atoi(fields[i]); err == nil {
				if (n >= 40 && n <= 47) || (n >= 100 && n <= 107) {
					return true
				}
			}
		}
	}
	return false
}

// dockModeChipFill returns the text the dock's mode chip paints its background
// across: the first run carrying an SGR that sets one. Read back off the
// rendered dock rather than off the label the renderer was handed, because the
// padding only matters as the fill the user sees.
func dockModeChipFill(t *testing.T, dock string) string {
	t.Helper()
	for i := 0; i < len(dock); i++ {
		if dock[i] != 0x1b || i+1 >= len(dock) || dock[i+1] != '[' {
			continue
		}
		end := strings.IndexByte(dock[i:], 'm')
		if end < 0 {
			break
		}
		end += i
		if !sgrSetsBackground(dock[i+2 : end]) {
			continue
		}
		text := dock[end+1:]
		if next := strings.IndexByte(text, 0x1b); next >= 0 {
			text = text[:next]
		}
		return text
	}
	t.Fatal("no background-filled run in the dock; the mode chip was not rendered")
	return ""
}

// TestDockModeChipHasSymmetricPadding is the regression for the chip whose fill
// ended flush against its last glyph.
//
// The mode icons are each written with their own padding (" X "), and the
// renderer then appended the split direction and the zoom flag after it. Those
// suffixes landed outside the trailing space, so the background stopped on the
// glyph and the chip read as clipped on its right edge only. "SIDEBAR" had
// never carried any padding at all.
func TestDockModeChipHasSymmetricPadding(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *OS)
	}{
		{"window mode tiling", func(m *OS) {
			m.Mode = WindowManagementMode
			m.AutoTiling = true
		}},
		{"terminal mode tiling", func(m *OS) {
			m.Mode = TerminalMode
			m.AutoTiling = true
		}},
		{"window mode floating", func(m *OS) {
			m.Mode = WindowManagementMode
			m.AutoTiling = false
		}},
		{"terminal mode floating", func(m *OS) {
			m.Mode = TerminalMode
			m.AutoTiling = false
		}},
		{"zoomed pane", func(m *OS) {
			m.Mode = WindowManagementMode
			m.AutoTiling = true
			m.Windows[0].Zoomed = true
		}},
		{"rail focused", func(m *OS) {
			m.SidebarFocused = true
		}},
		{"copy mode", func(m *OS) {
			m.Mode = TerminalMode
			m.Windows[0].CopyMode = &terminal.CopyMode{Active: true, State: terminal.CopyModeNormal}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := notifTestOS(t, 120)
			tc.setup(m)

			dock, _ := m.renderDockString()
			fill := dockModeChipFill(t, dock)

			if strings.TrimSpace(fill) == "" {
				t.Fatalf("mode chip fill carries no label: %q", fill)
			}
			if !strings.HasPrefix(fill, " ") {
				t.Errorf("mode chip fill starts flush against the glyph: %q", fill)
			}
			if !strings.HasSuffix(fill, " ") {
				t.Errorf("mode chip fill ends flush against the glyph: %q", fill)
			}
			// One cell either side, not a drifting amount as suffixes accumulate.
			if got := len(fill) - len(strings.TrimLeft(fill, " ")); got != 1 {
				t.Errorf("mode chip has %d cells of left padding, want 1: %q", got, fill)
			}
			if got := len(fill) - len(strings.TrimRight(fill, " ")); got != 1 {
				t.Errorf("mode chip has %d cells of right padding, want 1: %q", got, fill)
			}
		})
	}
}
