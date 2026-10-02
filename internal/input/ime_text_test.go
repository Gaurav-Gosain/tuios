package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// typeFromHost decodes host the way Bubble Tea does, runs every key press it
// holds through the input handler with a focused pane in terminal mode whose
// emulator was fed paneFlags, and returns the bytes that reached the pane.
func typeFromHost(t *testing.T, paneFlags, host string) string {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 160, 40
	o.EffectiveWidth, o.EffectiveHeight = 160, 40
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if paneFlags != "" {
		_, _ = em.Write([]byte(paneFlags))
	}
	pty := &capturePty{}
	o.Windows = []*terminal.Window{{ID: "ime", Terminal: em, Pty: pty, Width: 82, Height: 26, Workspace: 1}}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	o.Mode = app.TerminalMode
	// A kitty host that sends releases itself, as one does under these flags.
	o.KeyboardFlags = hostReportsEvents

	var d uv.EventDecoder
	buf := []byte(host)
	for len(buf) > 0 {
		n, ev := d.Decode(buf)
		buf = buf[n:]
		if k, ok := ev.(uv.KeyPressEvent); ok {
			HandleInput(tea.KeyPressMsg(k), o)
		}
	}
	return string(pty.got)
}

// TestInputMethodTextReachesKittyPane is issue #255: in v0.8.0 a Chinese
// input method could type nothing into Claude Code in a pane. Claude Code
// pushes CSI >5u (disambiguate and alternate keys), and tuios sent the text
// "，" to it as the key CSI 65292u, which it does not insert. Under
// disambiguate, text is sent as text; only report-all-keys makes it an escape
// code, and then with the text alongside.
//
// The host rows are what a macOS terminal sends for an input method commit
// while tuios asks it for CSI =5u: iTerm2, Terminal.app, kitty, WezTerm and
// Ghostty send the text as UTF-8. The CSI u rows are the escape-code forms a
// terminal may use for the same key, with and without the base-layout key.
//
// Negative control: v0.8.0 sends CSI 65292u for the first row under >1u and
// >5u.
func TestInputMethodTextReachesKittyPane(t *testing.T) {
	const (
		legacy       = ""
		disambiguate = "\x1b[>1u"
		claudeCode   = "\x1b[>5u\x1b[>4;2m"
		all          = "\x1b[>31u"
	)
	cases := []struct {
		name, host string
		want       map[string]string
	}{
		{"full-width comma as text", "，", map[string]string{
			legacy: "，", disambiguate: "，", claudeCode: "，", all: "\x1b[65292;1;65292u",
		}},
		{"full-width question mark as text", "？", map[string]string{
			legacy: "？", disambiguate: "？", claudeCode: "？", all: "\x1b[65311;1;65311u",
		}},
		{"ideographs as text", "你好", map[string]string{
			legacy: "你好", disambiguate: "你好", claudeCode: "你好",
			all: "\x1b[20320;1;20320u\x1b[22909;1;22909u",
		}},
		{"emoji as text", "👍", map[string]string{
			legacy: "👍", disambiguate: "👍", claudeCode: "👍", all: "\x1b[128077;1;128077u",
		}},
		{"joined emoji as text", "👨‍👩‍👧", map[string]string{
			legacy: "👨‍👩‍👧", disambiguate: "👨‍👩‍👧", claudeCode: "👨‍👩‍👧",
			all: "👨‍👩‍👧",
		}},
		{"full-width comma as a key", "\x1b[65292u", map[string]string{
			legacy: "，", disambiguate: "，", claudeCode: "，", all: "\x1b[65292;1;65292u",
		}},
		{"full-width comma with its base key", "\x1b[65292::44u", map[string]string{
			legacy: "，", disambiguate: "，", claudeCode: "，",
		}},
		{"comma key with full-width text", "\x1b[44;1;65292u", map[string]string{
			legacy: "，", disambiguate: "，", claudeCode: "，", all: "\x1b[44;1;65292u",
		}},
		{"shift slash with full-width text", "\x1b[47:63;2;65311u", map[string]string{
			legacy: "？", disambiguate: "？", claudeCode: "？", all: "\x1b[47:63;2;65311u",
		}},
	}
	for _, c := range cases {
		for flags, want := range c.want {
			if got := typeFromHost(t, flags, c.host); got != want {
				t.Errorf("%s, pane flags %q: host %q reached the pane as %q, want %q",
					c.name, flags, c.host, got, want)
			}
		}
	}
}
