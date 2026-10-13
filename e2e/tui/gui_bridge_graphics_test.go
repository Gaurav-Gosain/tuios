package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// graphicsProbe asks the terminal it runs in for kitty graphics and for DA1,
// with the tty raw, and prints what came back: KITTY-OK when the kitty query
// is answered OK, and DA1-SIXEL when DA1 lists sixel (4).
const graphicsProbe = `import os, select, sys, termios, tty
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
def ask(q, end):
    os.write(1, q)
    buf = b""
    while end not in buf:
        r, _, _ = select.select([fd], [], [], 3)
        if not r:
            break
        buf += os.read(fd, 256)
    return buf
try:
    kitty = ask(b"\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\", b"\x1b\\")
    da1 = ask(b"\x1b[c", b"c")
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, old)
out = []
if b";OK" in kitty:
    out.append("KITTY-OK")
params = da1.split(b"[?")[-1].rstrip(b"c").split(b";") if b"[?" in da1 else []
if b"4" in params:
    out.append("DA1-SIXEL")
print("PROBE " + " ".join(out) + " END", flush=True)
`

// TestGUIBridgeAnswersGraphicsQueries runs a program in a pane through the
// bridge that asks for kitty graphics and reads DA1. The GUI draws kitty
// images and sixel from the stream, so the daemon must answer the kitty query
// OK and list sixel in DA1. The bridge used to connect with the capabilities
// of its own model, which draws nothing, and icat and yazi drew nothing in
// the GUI.
func TestGUIBridgeAnswersGraphicsQueries(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("needs python3")
	}
	base := t.TempDir()
	probe := filepath.Join(base, "probe.py")
	if err := os.WriteFile(probe, []byte(graphicsProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	b := startBridge(t, base, "gfx", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	st := b.waitState(func(s *wireState) bool { return len(s.Windows) == 1 }, "one pane")
	pty := st.Windows[0].PTY
	b.input(pty, "python3 "+probe+"\r")
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], []byte(" END")) }, shellTimeout, "the probe's answer")
	b.mu.Lock()
	out := append([]byte(nil), b.outputs[pty]...)
	b.mu.Unlock()
	line := out[bytes.LastIndex(out, []byte("PROBE ")):]
	line = line[:bytes.Index(line, []byte(" END"))]
	t.Logf("probe: %q", line)
	saveTransferArtifact(t, "gui-bridge-graphics", map[string]any{"probe": string(line)})
	if !bytes.Contains(line, []byte("KITTY-OK")) {
		t.Errorf("ASSERTION: the kitty graphics query through the bridge was not answered OK")
	}
	if !bytes.Contains(line, []byte("DA1-SIXEL")) {
		t.Errorf("ASSERTION: DA1 through the bridge does not list sixel")
	}
	b.saveLog("graphics-queries")
}
