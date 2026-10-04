package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaneModifyOtherKeys: a program that asks for xterm's modifyOtherKeys
// (XTMODKEYS, CSI > 4 ; n m), as vim does at start, reads a modified key the
// legacy encoding cannot tell apart as CSI 27 ; modifier ; code ~. Ctrl+Enter
// is otherwise a line feed, Ctrl+; is otherwise a bare semicolon, and Ctrl+A
// stays 0x01 at level 1 and is encoded at level 2. A program that also pushed
// kitty keyboard flags reads CSI u instead: the kitty protocol wins.
//
// The host sends each key as kitty CSI u, the way a kitty-protocol terminal
// reports it, so tuios knows exactly which key was pressed.
//
// It runs in the standalone TUI and against a daemon, where the client
// encodes the key from its own copy of the pane's state.
//
// How this could pass wrongly, written down first:
//   - the program might read CSI 27 for every key whatever it asked for, so
//     the plain step asks for nothing and must read the legacy bytes;
//   - the kitty step could pass on a build that ignores modifyOtherKeys
//     altogether, so it is the level 2 step that has to read CSI 27;
//   - level 1 and level 2 could be the same, so Ctrl+A tells them apart.
//
// Negative control: with the EncodeModifyOtherKeys call cut from paneKeyBytes
// in internal/input/pane_key.go, the level1 and level2 steps fail in both
// subtests, and the plain and kitty steps pass.
func TestPaneModifyOtherKeys(t *testing.T) {
	const (
		ctrlEnter = "\x1b[13;5u"
		ctrlSemi  = "\x1b[59;5u"
		ctrlA     = "\x1b[97;5u"
	)
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, base := start(t, startOpts{cols: 120, rows: 30, daemonDefault: daemon})
			if daemon {
				killDaemon(t, base)
			}
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)

			dir := t.TempDir()
			art := artifactDir(t)
			for _, step := range []struct {
				name, setup, want string
			}{
				{"plain", "", "\n;\x01"},
				{"level1", "\\033[>4;1m", "\x1b[27;5;13~\x1b[27;5;59~\x01"},
				{"level2", "\\033[>4;2m", "\x1b[27;5;13~\x1b[27;5;59~\x1b[27;5;97~"},
				// The flags are popped when the step ends, so the shell after
				// it reads keys as before.
				{"kitty", "\\033[>4;2m\\033[>1u", ctrlEnter + ctrlSemi + ctrlA},
			} {
				out := filepath.Join(dir, step.name)
				script := filepath.Join(dir, step.name+".sh")
				// Raw mode so the keys reach the file unchanged. With min 0
				// time 40, cat ends after four seconds of silence.
				body := "stty raw -echo min 0 time 40\n" +
					"printf '" + step.setup + "'\n" +
					"printf 'KEYS%sREADY\\r\\n' " + step.name + "\n" +
					"cat > " + out + "\n" +
					"printf '\\033[>4;0m\\033[<u'\n" +
					"stty sane\n" +
					"echo " + splitMarker("PROBEDONE"+step.name) + "\n"
				if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := term.SendKeys("sh "+script, tuitest.Enter); err != nil {
					t.Fatalf("run the %s probe: %v", step.name, err)
				}
				if err := term.WaitForText("KEYS"+step.name+"READY", shellTimeout); err != nil {
					t.Fatalf("the %s probe never started: %v\n%s", step.name, err, term.Snapshot())
				}
				for _, k := range []string{ctrlEnter, ctrlSemi, ctrlA} {
					sendRaw(t, term, k)
					time.Sleep(insertGuard)
				}
				if err := term.WaitForText("PROBEDONE"+step.name, shellTimeout); err != nil {
					t.Fatalf("the %s probe never finished: %v\n%s", step.name, err, term.Snapshot())
				}
				raw, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("read the %s keys: %v", step.name, err)
				}
				_ = os.WriteFile(filepath.Join(art, step.name+".keys"), raw, 0o644)
				if string(raw) != step.want {
					t.Errorf("%s: the pane read %q, want %q", step.name, raw, step.want)
				}
			}
		})
	}
}
