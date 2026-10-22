package tuie2e

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// pastePrompt is the shell prompt the pane under every input shows. A paste
// that leaks to the pane lands after it, so the pane's last line reading
// exactly this is the proof that nothing reached the shell.
const pastePrompt = "PST>"

// TestPasteIntoInputBoxes pastes into every kind of input box from terminal
// mode, where a paste nothing claims is typed into the focused pane. Each
// field gets a bracketed paste holding a line break and a tab, and must show
// it on one line with single spaces, while the pane's shell must show nothing
// of it. A list that takes no text drops the paste, and the pane still gets
// nothing. The window rename is committed and read back from list-windows,
// and the rename also takes the paste key (ctrl+shift+v and ctrl+v), whose
// clipboard read a stand-in host terminal answers over OSC 52.
//
// How this could pass wrongly: a field that dropped the paste would leave the
// pane clean too, so every field must show the pasted text. A field that took
// the paste while the pane also got it would show the text, so every case
// reads the pane back after the field has the text, and again after the
// input closes.
func TestPasteIntoInputBoxes(t *testing.T) {
	const clip = "CLIP name"
	resp := &osc52Responder{replyB: base64.StdEncoding.EncodeToString([]byte(clip))}

	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renameClearSession, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", renameClearSession, err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", renameClearSession, "--name", "oldname"); err != nil {
		t.Fatalf("name the window: %v: %s", err, out)
	}
	win := focusedIn(t, base, renameClearSession)
	if out, err := tuiosCLI(t, base, "send-text", "-s", renameClearSession, "-w", win, "PS1='"+pastePrompt+" '"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", renameClearSession, "-w", win, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	paneLast := func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", renameClearSession, "-w", win)
		last := ""
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				last = strings.TrimRight(line, " ")
			}
		}
		return last
	}
	waitPane := func(what string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for paneLast() != pastePrompt {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the pane's last line is %q, want %q", what, paneLast(), pastePrompt)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// paneClean fails when any word of a paste is anywhere in the pane. A
	// leaked paste with a line break in it runs as a command and leaves a
	// fresh prompt, so the whole pane is read, not its last line, and every
	// word is looked for, since a leak can lose a character on its way.
	paneClean := func(t *testing.T, what, pasted string) {
		t.Helper()
		// A leaked paste is written to the pane at once, and the shell echoes
		// it well inside this.
		time.Sleep(700 * time.Millisecond)
		out, err := tuiosCLI(t, base, "capture-pane", "-s", renameClearSession, "-w", win)
		if err != nil {
			t.Fatalf("capture-pane: %v\n%s", err, out)
		}
		for _, word := range strings.Fields(pasted) {
			if strings.Contains(out, word) {
				t.Fatalf("%s reached the pane, which shows %q:\n%s", what, word, out)
			}
		}
	}
	waitPane("the prompt was never set")

	term := startIn(t, base, startOpts{args: []string{"attach", renameClearSession}, out: resp})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	art := artifactDir(t)
	// settle leaves whatever a failed case left open, and the shell line
	// empty, so the next case starts from terminal mode with nothing open.
	// toTerminalMode puts the client in terminal mode from either mode.
	// alt+esc leaves terminal mode and says so, but says nothing in window
	// mode, so it is not waited on: after it the i that enters terminal mode
	// cannot reach the shell.
	toTerminalMode := func(t *testing.T) {
		t.Helper()
		sendKeys(t, term, tuitest.Alt(tuitest.Esc))
		time.Sleep(insertGuard)
		enterTerminalMode(t, term)
	}
	settle := func(t *testing.T) {
		if !t.Failed() {
			return
		}
		_ = term.SendKeys(tuitest.Esc)
		time.Sleep(insertGuard)
		_ = term.SendKeys(tuitest.Esc)
		_, _ = tuiosCLI(t, base, "send-keys", "-s", renameClearSession, "-w", win, "C-u")
		time.Sleep(time.Second)
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	// The paste reaches the pane with nothing open, which is what makes the
	// pane clean afterwards mean something.
	if err := term.Paste("pz0"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	for deadline := time.Now().Add(uiTimeout); paneLast() != pastePrompt+" pz0"; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a paste with no input open never reached the pane: last line %q", paneLast())
		}
	}
	sendKeys(t, term, tuitest.Ctrl('u'))
	waitPane("ctrl+u did not clear the shell line")

	cases := []struct {
		name string
		// open opens the input from terminal mode and waits for it.
		open func(t *testing.T)
		// paste is what is pasted, want what the field must then show.
		paste, want string
		// close leaves the input; gone is on screen only while it is open.
		close []any
		gone  string
	}{
		{
			name: "window rename",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), "r")
				waitRenameField(t, term, "the rename dialog opened on the window's name", "oldname")
				sendKeys(t, term, tuitest.Ctrl('u'))
				waitRenameField(t, term, "ctrl+u emptied the field", "")
			},
			paste: "wr1 wra\nwrb\twrc\r\n",
			want:  "wr1 wra wrb wrc",
			close: []any{tuitest.Enter},
			gone:  "╭─ rename",
		},
		{
			// The session switcher's rename keeps terminal mode, so a paste
			// nothing claims there types into the pane under it.
			name: "session rename",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), "S")
				waitScreen(t, term, "the session switcher never opened", renameClearSession)
				sendKeys(t, term, tuitest.Ctrl('r'))
				waitRenameField(t, term, "the rename dialog opened on the session's name", renameClearSession)
				sendKeys(t, term, tuitest.Ctrl('u'))
				waitRenameField(t, term, "ctrl+u emptied the field", "")
			},
			paste: "sr1\nsr2",
			want:  "sr1 sr2",
			close: []any{tuitest.Esc, tuitest.Esc},
			gone:  "sr1 sr2",
		},
		{
			name: "workspace rename",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), "w", "r")
				if err := term.WaitFor(func(s tuitest.Screen) bool { _, ok := renameField(s); return ok }, uiTimeout); err != nil {
					t.Fatalf("the workspace rename never opened: %v\n%s", err, term.Snapshot())
				}
				sendKeys(t, term, tuitest.Ctrl('u'))
				waitRenameField(t, term, "ctrl+u emptied the field", "")
			},
			paste: "ws1\nws2",
			want:  "ws1 ws2",
			close: []any{tuitest.Esc},
			gone:  "╭─ rename",
		},
		{
			name: "command palette",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('p'))
				waitScreen(t, term, "the palette never opened", "Command Palette")
			},
			paste: "cp1\ncp2",
			want:  "cp1 cp2",
			close: []any{tuitest.Esc},
			gone:  "Command Palette",
		},
		{
			name: "help search",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), "?")
				waitScreen(t, term, "the help never opened", "Keybindings")
				sendKeys(t, term, "/")
			},
			paste: "hs1\nhs2",
			want:  "hs1 hs2",
			close: []any{tuitest.Esc, tuitest.Esc},
			gone:  "Keybindings",
		},
		{
			name: "session switcher",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), "S")
				waitScreen(t, term, "the session switcher never opened", renameClearSession)
			},
			paste: "ss1\nss2",
			want:  "ss1 ss2",
			close: []any{tuitest.Esc},
			gone:  "ss1 ss2",
		},
		{
			name: "settings search",
			open: func(t *testing.T) {
				sendKeys(t, term, tuitest.Ctrl('b'), ",")
				waitScreen(t, term, "settings never opened", "Settings")
				sendKeys(t, term, "/")
			},
			paste: "st1\nst2",
			want:  "st1 st2",
			close: []any{tuitest.Esc, tuitest.Esc},
			gone:  "st1 st2",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer settle(t)
			// Every input opens from terminal mode, where a paste nothing
			// claims goes to the pane. Some of them leave it as they open.
			toTerminalMode(t)
			c.open(t)
			if err := term.Paste(c.paste); err != nil {
				t.Fatalf("paste: %v", err)
			}
			paneClean(t, "a paste into the "+c.name, c.paste)
			waitScreen(t, term, "the field never showed the paste on one line", c.want)
			saveArtifact(t, term, art, "paste-"+strings.ReplaceAll(c.name, " ", "-"))
			sendKeys(t, term, c.close...)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), c.gone) }, uiTimeout); err != nil {
				t.Fatalf("the %s never closed: %v\n%s", c.name, err, term.Snapshot())
			}
			paneClean(t, "closing the "+c.name, c.paste)
			if c.name == "window rename" {
				waitWindowName(t, base, term, c.want)
			}
		})
	}

	// A list that takes no text drops the paste: the help with no search line.
	t.Run("help list drops it", func(t *testing.T) {
		defer settle(t)
		toTerminalMode(t)
		sendKeys(t, term, tuitest.Ctrl('b'), "?")
		waitScreen(t, term, "the help never opened", "Keybindings")
		if err := term.Paste("hl1\nhl2"); err != nil {
			t.Fatalf("paste: %v", err)
		}
		paneClean(t, "a paste into the help's list", "hl1 hl2")
		sendKeys(t, term, tuitest.Esc)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Keybindings") }, uiTimeout); err != nil {
			t.Fatalf("the help never closed: %v\n%s", err, term.Snapshot())
		}
		paneClean(t, "closing the help", "hl1 hl2")
	})

	// The paste key asks the host terminal for its clipboard, and the answer
	// goes into the field. Both keys: the one bound to terminal_paste_host,
	// and ctrl+v, the Inbox editors' paste key.
	for _, k := range []struct {
		name string
		key  any
	}{
		{"ctrl+shift+v", tuitest.Key("\x1b[118;6u")},
		{"ctrl+v", tuitest.Ctrl('v')},
	} {
		t.Run("rename paste key "+k.name, func(t *testing.T) {
			defer settle(t)
			toTerminalMode(t)
			sendKeys(t, term, tuitest.Ctrl('b'), "r")
			if err := term.WaitFor(func(s tuitest.Screen) bool { _, ok := renameField(s); return ok }, uiTimeout); err != nil {
				t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
			}
			sendKeys(t, term, tuitest.Ctrl('u'))
			waitRenameField(t, term, "ctrl+u emptied the field", "")
			resp.mu.Lock()
			resp.replied = false
			resp.mu.Unlock()
			resp.arm(term)
			sendKeys(t, term, k.key)
			waitRenameField(t, term, "the paste key's clipboard went into the field", clip)
			saveArtifact(t, term, art, "paste-key-"+strings.ReplaceAll(k.name, "+", "-"))
			paneClean(t, "the paste key's clipboard", "CLIP")
			sendKeys(t, term, tuitest.Esc)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !renameDialogUp(s) }, uiTimeout); err != nil {
				t.Fatalf("esc did not close the rename: %v\n%s", err, term.Snapshot())
			}
		})
	}

	alive(t, term, "after pasting into every input box")
}
