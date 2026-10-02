package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A Wayland compositor in a pane pushes the kitty keyboard protocol's
// disambiguate, event-type, alternate-key and all-keys flags (CSI >15u) and turns
// every report into a wl_keyboard event. Its clients hold a key down from the
// press until the release, and their xkb repeats it all that time, so the pane
// must be told about every release, every repeat and the lock keys' state
// exactly as a kitty host would tell it. These tests run ./keyecho in a pane,
// which logs the bytes the pane receives.

// hostAnswer is the host terminal's reply to the flag query (CSI ? u), which
// tells tuios what the host now reports. tuios reads it from its input like a
// key.
func hostAnswer(flags int) string { return fmt.Sprintf("\x1b[?%du", flags) }

// TestPaneGetsReleaseTheHostCannotSend: the host has not granted event types
// (it never answered, or it answered without them). That is the moment after
// the pane is focused and before the host has switched, and it is every key on a
// host without the protocol. The host will send no release, so tuios must send
// one with the press. Without it the compositor held the key down and its client
// typed the letter until the next key came.
func TestPaneGetsReleaseTheHostCannotSend(t *testing.T) {
	term, log := startKeyEcho(t)

	// Disambiguate, alternate keys and all keys: no event types.
	sendRaw(t, term, hostAnswer(13))
	time.Sleep(insertGuard)
	sendRaw(t, term, "x")
	waitPaneBytes(t, term, log, "\x1b[120u\x1b[120;1:3u")
}

// TestPaneGetsRepeatAsRepeat: a host that reports event types sends a held key's
// repeats as event type 2. The pane asked for event types, so it must see a
// repeat, not a second press of a key that is already down.
func TestPaneGetsRepeatAsRepeat(t *testing.T) {
	term, log := startKeyEcho(t)

	sendRaw(t, term, hostAnswer(31))
	time.Sleep(insertGuard)
	sendRaw(t, term, "\x1b[97u", "\x1b[97;1:2u", "\x1b[97;1:2u", "\x1b[97;1:3u")
	waitPaneBytes(t, term, log, "\x1b[97u\x1b[97;1:2u\x1b[97;1:2u\x1b[97;1:3u")
}

// TestPaneGetsLockState: kitty puts Caps Lock (64) and Num Lock (128) in the
// modifier field of every key it reports as an escape code. A pane that asked
// for every key must get them too. A compositor reads them to bring its own
// lock state in line with the host's, which it cannot otherwise know when the
// desktop turned Num Lock on before the pane had the keyboard. Without them the
// keypad typed KP_End where the user typed 1.
func TestPaneGetsLockState(t *testing.T) {
	term, log := startKeyEcho(t)

	sendRaw(t, term, hostAnswer(31))
	time.Sleep(insertGuard)
	// Num Lock on: a letter, then keypad 1.
	sendRaw(t, term, "\x1b[97;129u", "\x1b[97;129:3u", "\x1b[57400;129u", "\x1b[57400;129:3u")
	waitPaneBytes(t, term, log, "\x1b[97;129u\x1b[97;129:3u\x1b[57400;129u\x1b[57400;129:3u")
}

// startKeyEcho boots tuios, opens a pane, and runs keyecho in it with the
// compositor's flags. It returns the terminal and the path of the log of bytes
// the pane receives.
func startKeyEcho(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	bin := buildKeyEcho(t)
	base := t.TempDir()
	log := filepath.Join(base, "pane-bytes")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "a pane to run keyecho in")
	enterTerminalMode(t, term)
	runInShell(t, term, "stty raw -echo; "+bin+" 15 "+log, "KEYECHO-READY", shellTimeout)
	// The push reaches the pane's emulator with the output that carried the
	// marker, so the flags are in effect by now.
	return term, log
}

func sendRaw(t *testing.T, term *tuitest.Terminal, seqs ...string) {
	t.Helper()
	for _, s := range seqs {
		if err := term.SendKeys(tuitest.Key(s)); err != nil {
			t.Fatalf("send %q: %v", s, err)
		}
	}
}

// waitPaneBytes waits until the pane has received exactly want, and fails with
// what it got instead.
func waitPaneBytes(t *testing.T, term *tuitest.Terminal, log, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var got string
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(log)
		got = string(b)
		if got == want {
			return
		}
		if len(got) > len(want) || !strings.HasPrefix(want, got) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Anything that was still on its way.
	time.Sleep(300 * time.Millisecond)
	b, _ := os.ReadFile(log)
	got = string(b)
	if got != want {
		t.Fatalf("the pane received %q, want %q\n%s", got, want, term.Snapshot())
	}
}

// buildKeyEcho compiles the key logger once per test binary.
func buildKeyEcho(t *testing.T) string {
	t.Helper()
	keyEchoOnce.Do(func() {
		dir, err := os.MkdirTemp("", "keyecho")
		if err != nil {
			keyEchoErr = err
			return
		}
		bin := filepath.Join(dir, "keyecho")
		build := exec.Command("go", "build", "-o", bin, "./keyecho")
		if out, err := build.CombinedOutput(); err != nil {
			keyEchoErr = fmt.Errorf("build keyecho: %v\n%s", err, out)
			return
		}
		keyEchoBin = bin
	})
	if keyEchoErr != nil {
		t.Fatalf("%v", keyEchoErr)
	}
	return keyEchoBin
}

var (
	keyEchoOnce sync.Once
	keyEchoBin  string
	keyEchoErr  error
)
