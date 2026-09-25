package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// TestHelpKeysAgreeAcrossModes drives the help overlay's keys through both
// mode handlers. Each mode used to carry its own copy of the help handler and
// the two had drifted: q was typed into the search in terminal mode but closed
// help outside search only in window mode, and ? closed help while searching in
// terminal mode but only left the search in window mode. The same key now does
// the same thing in both, following the footer hints.
func TestHelpKeysAgreeAcrossModes(t *testing.T) {
	type want struct {
		showHelp   bool
		searchMode bool
		query      string
	}
	cases := []struct {
		name      string
		searching bool
		query     string
		msg       tea.KeyPressMsg
		want      want
	}{
		{"esc closes", false, "", tea.KeyPressMsg{Code: tea.KeyEscape}, want{false, false, ""}},
		{"esc leaves search", true, "ab", tea.KeyPressMsg{Code: tea.KeyEscape}, want{true, false, ""}},
		{"? closes", false, "", tea.KeyPressMsg{Code: '?', Text: "?"}, want{false, false, ""}},
		{"? closes while searching", true, "ab", tea.KeyPressMsg{Code: '?', Text: "?"}, want{false, false, ""}},
		{"q closes", false, "", tea.KeyPressMsg{Code: 'q', Text: "q"}, want{false, false, ""}},
		{"q is typed while searching", true, "ab", tea.KeyPressMsg{Code: 'q', Text: "q"}, want{true, true, "abq"}},
		{"letter is typed while searching", true, "", tea.KeyPressMsg{Code: 'n', Text: "n"}, want{true, true, "n"}},
		{"letter is swallowed outside search", false, "", tea.KeyPressMsg{Code: 'n', Text: "n"}, want{true, false, ""}},
		{"slash enters search", false, "", tea.KeyPressMsg{Code: '/', Text: "/"}, want{true, true, ""}},
		{"backspace removes one rune", true, "aé", tea.KeyPressMsg{Code: tea.KeyBackspace}, want{true, true, "a"}},
	}

	modes := []struct {
		name   string
		mode   app.Mode
		handle func(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd)
	}{
		{"terminal", app.TerminalMode, HandleTerminalModeKey},
		{"window", app.WindowManagementMode, HandleWindowManagementModeKey},
	}

	for _, mode := range modes {
		for _, tc := range cases {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				m := helpModalOS(t)
				m.Mode = mode.mode
				m.HelpSearchMode = tc.searching
				m.HelpSearchQuery = tc.query

				out, _ := mode.handle(tc.msg, m)
				got := want{out.ShowHelp, out.HelpSearchMode, out.HelpSearchQuery}
				if got != tc.want {
					t.Errorf("got %+v, want %+v", got, tc.want)
				}
				if len(out.Windows) != 2 {
					t.Errorf("the key reached the window manager: %d windows", len(out.Windows))
				}
			})
		}

		t.Run(mode.name+"/down then up scrolls", func(t *testing.T) {
			m := helpModalOS(t)
			m.Mode = mode.mode
			out, _ := mode.handle(tea.KeyPressMsg{Code: tea.KeyDown}, m)
			if out.HelpScrollOffset != 2 {
				t.Fatalf("down scrolled to %d, want 2", out.HelpScrollOffset)
			}
			out, _ = mode.handle(tea.KeyPressMsg{Code: tea.KeyUp}, out)
			if out.HelpScrollOffset != 0 {
				t.Errorf("up scrolled to %d, want 0", out.HelpScrollOffset)
			}
		})
	}
}

// TestCacheStatsKeysAgreeAcrossModes covers the other overlay both modes used
// to copy: q, esc and c close it and other keys are swallowed.
func TestCacheStatsKeysAgreeAcrossModes(t *testing.T) {
	modes := []struct {
		name   string
		mode   app.Mode
		handle func(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd)
	}{
		{"terminal", app.TerminalMode, HandleTerminalModeKey},
		{"window", app.WindowManagementMode, HandleWindowManagementModeKey},
	}
	for _, mode := range modes {
		for _, key := range []string{"q", "c", "n"} {
			t.Run(mode.name+"/"+key, func(t *testing.T) {
				m := helpModalOS(t)
				m.Mode = mode.mode
				m.ShowHelp = false
				m.ShowCacheStats = true
				out, _ := mode.handle(tea.KeyPressMsg{Code: rune(key[0]), Text: key}, m)
				wantOpen := key == "n"
				if out.ShowCacheStats != wantOpen {
					t.Errorf("ShowCacheStats = %v, want %v", out.ShowCacheStats, wantOpen)
				}
				if len(out.Windows) != 2 {
					t.Errorf("the key reached the window manager: %d windows", len(out.Windows))
				}
			})
		}
	}
}
