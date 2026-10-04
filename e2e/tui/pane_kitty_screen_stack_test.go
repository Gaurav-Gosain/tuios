package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaneKittyFlagsPerScreen: the kitty keyboard protocol gives the main and
// the alternate screen a flag stack each. Flags pushed on the main screen do
// not follow a program onto the alternate one, so a full-screen program that
// never asked for the protocol reads Ctrl+A as 0x01 there, even when the
// shell under it left flags pushed. Both screens used to share one stack, and
// that program read CSI 97 ; 5 u.
//
// It runs in the standalone TUI and against a daemon, where the client
// encodes the key from its own copy of the pane's state.
//
// How this could pass wrongly, written down first:
//   - the push might not reach tuios at all, so the main step pushes the same
//     flags, stays on the main screen, and must read CSI u;
//   - the alternate step could read 0x01 because nothing was pushed, so it
//     pushes on the main screen first, exactly as the main step does.
//
// Negative control: with the SetAltScreen call cut from setAltScreenMode in
// internal/vt/csi_mode.go, the alt step fails in the standalone subtest. The
// daemon client feeds the same emulator, so it fails there too.
func TestPaneKittyFlagsPerScreen(t *testing.T) {
	const ctrlA = "\x1b[97;5u"
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
				name, enter, leave, want string
			}{
				{"main", "", "", ctrlA},
				{"alt", "\\033[?1049h", "\\033[?1049l", "\x01"},
			} {
				out := filepath.Join(dir, step.name)
				script := filepath.Join(dir, step.name+".sh")
				// Raw mode so the key reaches the file unchanged. With min 0
				// time 40, cat ends after four seconds of silence. The flags
				// are pushed on the main screen in both steps and popped
				// there at the end.
				body := "stty raw -echo min 0 time 40\n" +
					"printf '\\033[>1u" + step.enter + "'\n" +
					"printf 'KITTY%sREADY\\r\\n' " + step.name + "\n" +
					"cat > " + out + "\n" +
					"printf '" + step.leave + "\\033[<u'\n" +
					"stty sane\n" +
					"echo " + splitMarker("PROBEDONE"+step.name) + "\n"
				if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := term.SendKeys("sh "+script, tuitest.Enter); err != nil {
					t.Fatalf("run the %s probe: %v", step.name, err)
				}
				if err := term.WaitForText("KITTY"+step.name+"READY", shellTimeout); err != nil {
					t.Fatalf("the %s probe never started: %v\n%s", step.name, err, term.Snapshot())
				}
				sendRaw(t, term, ctrlA)
				time.Sleep(insertGuard)
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
