package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// ctrlOn is a Ctrl chord as a terminal reports it with alternate keys: the
// key typed, and the US key at its position. On Dvorak b sits on US n, c on
// i, x on b, u on f, j on c. On QWERTZ ü sits on US [.
func ctrlOn(typed, us rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: typed, BaseCode: us, Mod: tea.ModCtrl}
}

// The leader is the chord the user typed, not the US key at its position.
func TestLatinChordIsTheLeader(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(ctrlOn('b', 'n'), o)
	if !o.PrefixActive {
		t.Fatal("Dvorak Ctrl+B (US n) did not arm the prefix")
	}
	if len(pty.got) != 0 {
		t.Fatalf("the pane got %q for the leader", pty.got)
	}
}

// ctrl+c after the leader is compared as a string, like every panel key.
func TestLatinCtrlCCancelsThePrefix(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(ctrlOn('b', 'n'), o)
	o, _ = HandleKeyPress(ctrlOn('c', 'i'), o)
	if o.PrefixActive {
		t.Fatal("ctrl+c left the prefix armed")
	}
	if len(pty.got) != 0 {
		t.Fatalf("ctrl+c after the leader reached the pane as %q", pty.got)
	}
}

// The showkeys strip shows the key typed.
func TestShowkeysNamesTheKeyTyped(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o.ShowKeys = true
	o, _ = HandleKeyPress(ctrlOn('b', 'n'), o)
	if len(o.RecentKeys) != 1 {
		t.Fatalf("showkeys recorded %d keys, want 1", len(o.RecentKeys))
	}
	got := o.RecentKeys[0]
	if got.Key != "b" || len(got.Modifiers) != 1 || got.Modifiers[0] != "Ctrl" {
		t.Fatalf("showkeys shows %v + %q for Ctrl+B, want Ctrl + b", got.Modifiers, got.Key)
	}
}

// The binding recorder saves the chord the user can press again.
func TestRecorderCapturesTheKeyTyped(t *testing.T) {
	o := unbindInputOS(t)
	o.KeybindArm()
	o, _ = HandleKeyPress(ctrlOn('j', 'c'), o)
	if key, _ := o.KeybindCaptured(); key != "ctrl+j" {
		t.Fatalf("the recorder captured %q for Ctrl+J, want ctrl+j", key)
	}
}

// ctrl+u clears a text field by comparing the chord's spelling.
func TestLatinCtrlUClearsAField(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	_ = o.OpenCommandPalette()
	o, _ = HandleKeyPress(press("a"), o)
	o, _ = HandleKeyPress(press("b"), o)
	if o.CommandPaletteQuery != "ab" {
		t.Fatalf("typed query is %q, want ab", o.CommandPaletteQuery)
	}
	o, _ = HandleKeyPress(ctrlOn('u', 'f'), o)
	if o.CommandPaletteQuery != "" {
		t.Fatalf("Ctrl+U left the query as %q", o.CommandPaletteQuery)
	}
}

// A pane is sent what the host sent, on either encoding. The decoded cases
// carry the key as Bubble Tea hands it over, with the base key copied into
// the shifted field.
func TestPaneGetsTheHostKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		msg   tea.KeyPressMsg
		flags string
		want  string
	}{
		{"alternate keys pane", ctrlOn('x', 'b'), pushAll, "\x1b[120::98;5u"},
		{"plain pane", ctrlOn('x', 'b'), "", "\x18"},
		{"non-Latin, alternate keys pane", ctrlOn('с', 'c'), pushAll, "\x1b[1089::99;5u"},
		{"qwertz ctrl+ü, plain pane", ctrlOn('ü', '['), "", "\x1b"},
		{"qwertz ctrl+ü, alternate keys pane", ctrlOn('ü', '['), pushAll, "\x1b[252::91;5u"},
		{"decoded ctrl+x, alternate keys pane",
			tea.KeyPressMsg{Code: 'x', ShiftedCode: 'b', BaseCode: 'b', Mod: tea.ModCtrl}, pushAll, "\x1b[120::98;5u"},
		{"decoded ctrl+shift+x, alternate keys pane",
			tea.KeyPressMsg{Code: 'x', ShiftedCode: 'b', BaseCode: 'b', Mod: tea.ModCtrl | tea.ModShift}, pushAll, "\x1b[120:88:98;6u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
			if tc.flags != "" {
				if _, err := o.Windows[0].Terminal.Write([]byte(tc.flags)); err != nil {
					t.Fatal(err)
				}
			}
			o, _ = HandleKeyPress(tc.msg, o)
			if o.PrefixActive {
				t.Fatal("the chord armed the prefix")
			}
			if got := string(pty.got); got != tc.want {
				t.Fatalf("the pane got %q, want %q", got, tc.want)
			}
		})
	}
}

// readKey keeps the base-layout key on a non-Latin key and on a key that is
// its own base, and takes it off everywhere else.
func TestReadKeyKeepsTheBaseLayoutKeyForNonLatinOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
		keep bool
	}{
		{"dvorak ctrl+b", ctrlOn('b', 'n'), false},
		{"german ctrl+z", ctrlOn('z', 'y'), false},
		{"german sharp s", tea.KeyPressMsg{Code: 'ß', BaseCode: '-', Text: "ß"}, false},
		{"ukrainian ctrl+и", ctrlOn('и', 'b'), true},
		{"ukrainian ш", tea.KeyPressMsg{Code: 'ш', BaseCode: 'i', Text: "ш"}, true},
		{"ш reported as its own base", tea.KeyPressMsg{Code: 'ш', BaseCode: 'ш', Text: "ш"}, true},
		{"b reported as its own base", tea.KeyPressMsg{Code: 'b', BaseCode: 'b', Mod: tea.ModCtrl}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readKey(tc.msg)
			if (got.BaseCode != 0) != tc.keep {
				t.Fatalf("readKey kept base %q = %v, want %v", got.BaseCode, got.BaseCode != 0, tc.keep)
			}
			if got.Code != tc.msg.Code || got.Mod != tc.msg.Mod || got.Text != tc.msg.Text {
				t.Fatal("readKey changed more than the base-layout key")
			}
		})
	}
}

// A key named in both fields, as the Windows console reports every key,
// still spells itself.
func TestReadKeyLeavesAKeyThatIsItsOwnBase(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"up arrow", tea.KeyPressMsg{Code: tea.KeyUp, BaseCode: tea.KeyUp}, "up"},
		{"left alt", tea.KeyPressMsg{Code: tea.KeyLeftAlt, BaseCode: tea.KeyLeftAlt}, "leftalt"},
		{"ctrl+b", tea.KeyPressMsg{Code: 'b', BaseCode: 'b', Mod: tea.ModCtrl}, "ctrl+b"},
		{"f5", tea.KeyPressMsg{Code: tea.KeyF5, BaseCode: tea.KeyF5}, "f5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readKey(tc.msg).Keystroke(); got != tc.want {
				t.Fatalf("readKey spells %q, want %q", got, tc.want)
			}
		})
	}
}
