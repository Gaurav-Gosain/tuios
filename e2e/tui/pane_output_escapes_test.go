package tuie2e

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// These tests cover what a pane's output may do outside the pane: reach the
// host terminal, the clipboard, or a shell through a paste or a saved layout.
// Each one prints the output a hostile file would hold, the way `cat` of that
// file would.

// paneHostCopy keeps a copy of everything tuios writes to the host terminal.
type paneHostCopy struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (h *paneHostCopy) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.Write(p)
}

func (h *paneHostCopy) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.String()
}

// A kitty OSC 99 notification with e=1 carries base64 text, which decodes to
// any bytes. The decoded ESC sequences must not reach the host terminal, from
// the forwarded notification or from the dock message that shows it.
func TestPaneNotifyPayloadWritesNoEscapesToHost(t *testing.T) {
	host := &paneHostCopy{}
	term, _ := start(t, startOpts{cols: 120, rows: 40, out: host})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// The decoded body: visible text, then an OSC 52 clipboard write and a
	// window title change that must never reach the host.
	body := "NOTEOK\x1b]52;c;UFdORUQ=\x07\x1b]2;PWNTITLE\x07tail"
	cmd := `printf '\033]99;e=1;` + base64.StdEncoding.EncodeToString([]byte(body)) + `\033\\'`
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("send printf: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "NOTEOK")
	}, shellTimeout); err != nil {
		t.Fatalf("the notification never reached the dock: %v\n%s", err, term.Snapshot())
	}
	// A beat for any host write that trails the frame.
	time.Sleep(300 * time.Millisecond)

	out := host.String()
	for _, bad := range []string{"\x1b]52;c;UFdORUQ=", "\x1b]2;PWNTITLE"} {
		if strings.Contains(out, bad) {
			t.Fatalf("a pane's notification wrote %q to the host terminal", bad)
		}
	}
	alive(t, term, "after an OSC 99 notification with control bytes")
}

// startCatV runs cat -v in the focused pane with bracketed paste turned on,
// so the screen shows every byte a paste delivers.
func startCatV(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(`printf '\033[?2004h'; echo CATREADY; cat -v`, tuitest.Enter); err != nil {
		t.Fatalf("start cat -v: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "CATREADY") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("cat -v never started: %v\n%s", err, term.Snapshot())
	}
}

// The clipboard can hold ESC[201~. tuios's own paste must drop the ESC, so
// the text cannot end the bracketed paste early and run as typed input.
func TestClipboardPasteCannotEndBracketedPaste(t *testing.T) {
	clip := "SAFE\x1b[201~echo PWN\n"
	resp := &osc52Responder{replyB: base64.StdEncoding.EncodeToString([]byte(clip))}
	term, _ := start(t, startOpts{cols: 120, rows: 40, out: resp})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	startCatV(t, term)

	resp.arm(term)
	if err := term.SendKeys(tuitest.Key("\x1b[118;6u")); err != nil {
		t.Fatalf("send ctrl+shift+v: %v", err)
	}
	if err := term.WaitForText("echo PWN", shellTimeout); err != nil {
		t.Fatalf("the paste never reached the pane: %v\n%s", err, term.Snapshot())
	}
	screen := term.Screen().Text()
	if strings.Contains(screen, "SAFE^[[201~") {
		t.Fatalf("the pasted text carried ESC[201~ to the pane:\n%s", term.Snapshot())
	}
	if !strings.Contains(screen, "^[[200~SAFE[201~echo PWN") {
		t.Fatalf("the paste did not arrive as one bracketed paste without ESC:\n%s", term.Snapshot())
	}
}

// A clipboard reply that tuios did not ask for is not typed into the pane.
func TestUnrequestedClipboardReplyIsNotPasted(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	startCatV(t, term)

	reply := base64.StdEncoding.EncodeToString([]byte("UNASKED\n"))
	if err := term.Type("\x1b]52;c;" + reply + "\x07"); err != nil {
		t.Fatalf("send clipboard reply: %v", err)
	}
	// A positive control after the reply, so the wait has something to find.
	if err := term.SendKeys("AFTERREPLY", tuitest.Enter); err != nil {
		t.Fatalf("type control line: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "AFTERREPLY") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the control line never came back from cat: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "UNASKED") {
		t.Fatalf("an unrequested clipboard reply was pasted into the pane:\n%s", term.Snapshot())
	}
}

// The focused pane may set the host clipboard. Text with a line break is the
// kind that runs when pasted, so the dock says it did.
func TestFocusedPaneOSC52WriteReachesHostWithMessage(t *testing.T) {
	host := &paneHostCopy{}
	term, _ := start(t, startOpts{cols: 120, rows: 40, out: host})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	payload := base64.StdEncoding.EncodeToString([]byte("YANKED\nNEXT"))
	if err := term.SendKeys(`printf '\033]52;c;`+payload+`\007'`, tuitest.Enter); err != nil {
		t.Fatalf("send printf: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "copied")
	}, shellTimeout); err != nil {
		t.Fatalf("the dock did not say the pane copied: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(host.String(), "]52;c;"+payload) {
		t.Fatalf("the focused pane's clipboard write never reached the host")
	}
}

// A layout saved from a pane that announced a directory holding $(...) must
// not run it when the layout is loaded.
func TestLayoutWithSpoofedCwdRunsNothing(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e-layout-cwd"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	marker := filepath.Join(base, "layout-pwned")
	// %%20 is a space once printf has read it, and OSC 7 carries a URL.
	osc := `printf '\033]7;file://localhost/tmp/x$(touch%%20` + marker + `)\033\\'; echo OSCSENT`
	if err := term.SendKeys(osc, tuitest.Enter); err != nil {
		t.Fatalf("send OSC 7: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "OSCSENT") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the OSC 7 line never ran: %v\n%s", err, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "run-command", "-s", "e2e-layout-cwd", "SaveLayout", "spoof"); err != nil {
		t.Fatalf("SaveLayout: %v\n%s", err, out)
	}
	saved, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "layouts", "spoof.json"))
	if err != nil {
		t.Fatalf("the layout was not saved: %v", err)
	}
	t.Logf("saved layout:\n%s", saved)

	if out, err := tuiosCLI(t, base, "run-command", "-s", "e2e-layout-cwd", "LoadLayout", "spoof"); err != nil {
		t.Fatalf("LoadLayout: %v\n%s", err, out)
	}
	// The shell gets the cd, if any, within a moment. Give it time to run.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("loading the layout ran the command in the pane's announced directory\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after loading a layout")
}

// whereIs runs a check in the focused pane's shell and waits for its answer:
// IN<NAME> when the shell is in a folder called name, NOT<NAME> when it is not.
// The quotes keep the answer from appearing in the typed line itself.
func whereIs(t *testing.T, term *tuitest.Terminal, name string) string {
	t.Helper()
	up := strings.ToUpper(name)
	cmd := `case $PWD in */` + name + `) echo IN""` + up + `;; *) echo NOT""` + up + `;; esac`
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type the check: %v", err)
	}
	var got string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		switch {
		case strings.Contains(txt, "NOT"+up):
			got = "NOT" + up
		case strings.Contains(txt, "IN"+up):
			got = "IN" + up
		}
		return got != ""
	}, shellTimeout); err != nil {
		t.Fatalf("the check never answered: %v\n%s", err, term.Snapshot())
	}
	return got
}

// In a daemon session, the default, a pane has no PTY on the client side. The
// sidebar's folder click with folder_click = "cd" asks the daemon to type the
// cd, and the daemon types it only when the pane's shell is at its prompt.
func TestDaemonFolderClickCdOnlyAtAPrompt(t *testing.T) {
	dir := fileViewFixture(t)
	base := t.TempDir()
	writeTapeConfigFile(t, base, "[appearance.sidebar]\nfolder_click = \"cd\"\n")
	term := startIn(t, base, startOpts{args: []string{"new", "e2e-cd-here"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf 'in-the-dir\\n'", "in-the-dir", uiTimeout)
	runInShell(t, term, `printf '\033]7;file://%s\033\\marked\n' "$PWD"`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText("alpha/", uiTimeout); err != nil {
		t.Fatalf("the files section never listed the folder: %v\n%s", err, term.Snapshot())
	}
	click := func(name string) {
		t.Helper()
		col, row, ok := findOnGrid(term.Screen(), name+"/")
		if !ok {
			t.Fatalf("no %s row to click:\n%s", name, term.Snapshot())
		}
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	}

	// At the prompt: the cd reaches the shell.
	click("alpha")
	time.Sleep(500 * time.Millisecond)
	enterTerminalMode(t, term)
	if got := whereIs(t, term, "alpha"); got != "INALPHA" {
		t.Fatalf("a folder click at a daemon pane's prompt did not cd there:\n%s", term.Snapshot())
	}
	runInShell(t, term, "cd "+dir+" && clear && echo back''home", "backhome", uiTimeout)

	// A program in the foreground: no cd, and the dock says why. The client
	// may already know from the daemon's report that sleep runs there, or the
	// daemon refuses when asked. Either way, nothing is typed.
	if err := term.SendKeys("sleep 30", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	leaveTerminalMode(t, term)
	click("zulu")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "did not type a cd") || strings.Contains(txt, "running in that pane")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock did not say the cd was not typed: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Key("\x03")); err != nil {
		t.Fatal(err)
	}
	if got := whereIs(t, term, "zulu"); got != "NOTZULU" {
		t.Fatalf("a folder click reached the shell under a program:\n%s", term.Snapshot())
	}
}
