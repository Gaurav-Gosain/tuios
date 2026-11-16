package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Connections from panes to the daemon's own socket are capped, and some
// slots are kept for the person.
//
// The failure modes this is written against:
//   - one pane opens connections and holds them until the daemon's 1024
//     slots are gone, so the person's own tuios commands are refused;
//   - one pane takes the slots every other pane needs;
//   - a pane that forks children, each in a session of its own with no
//     terminal, gets around a cap per pane and fills the pool;
//   - the caps refuse the person, or a pane that holds a few connections.

// connHolderScript writes a python3 program that opens n connections to the
// socket, sends "{" on each and holds them. A connection the daemon refuses
// is told why and closed, so it turns readable. Once the count of held ones
// stops changing, it writes that count to out+"."+tag. With forks, it first
// starts that many children, each in a new session with no terminal, which
// do the same under the tags c0, c1 and so on.
func connHolderScript(t *testing.T, dir string) string {
	t.Helper()
	body := `import os, select, socket, sys, time
sock, out, n, forks = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
def hold(tag):
    conns = []
    for _ in range(n):
        s = socket.socket(socket.AF_UNIX)
        try:
            s.connect(sock)
            s.sendall(b"{")
        except OSError:
            s.close()
            continue
        conns.append(s)
    last, stable, deadline = -1, 0, time.time() + 20
    while time.time() < deadline:
        p = select.poll()
        for s in conns:
            p.register(s, select.POLLIN | select.POLLHUP)
        ready = {fd for fd, _ in p.poll(0)}
        held = sum(1 for s in conns if s.fileno() not in ready)
        stable = stable + 1 if held == last else 0
        last = held
        if stable >= 10:
            break
        time.sleep(0.2)
    with open(out + "." + tag + ".tmp", "w") as f:
        f.write(str(last))
    os.rename(out + "." + tag + ".tmp", out + "." + tag)
    while True:
        time.sleep(3600)
for i in range(forks):
    if os.fork() == 0:
        os.setsid()
        hold("c" + str(i))
if forks == 0:
    hold("main")
while True:
    time.sleep(3600)
`
	path := filepath.Join(dir, "conn_holder.py")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// heldCount waits for the count a holder wrote under tag.
func heldCount(t *testing.T, out, tag string) int {
	t.Helper()
	text := waitFileHas(t, out+"."+tag, "the holder "+tag+" never wrote its count", 60*time.Second, func(s string) bool { return s != "" })
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		t.Fatalf("holder %s wrote %q", tag, text)
	}
	return n
}

// TestPanesCannotTakeThePersonsConnections runs three holders in panes of a
// real daemon: one pane asks for 100 connections, a second pane for 10, and
// 16 children with no terminal for 64 each. The person's tuios commands must
// still work.
func TestPanesCannotTakeThePersonsConnections(t *testing.T) {
	needPython(t)
	base := t.TempDir()
	killDaemon(t, base)
	var tr transcript
	if out, err := tuiosCLI(t, base, "new", "conns", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "conns", "second", "--no-focus"); err != nil {
		t.Fatalf("open a second pane: %v\n%s", err, out)
	}
	ids := grantWindowIDs(t, base, "conns")
	if len(ids) != 2 {
		t.Fatalf("want two panes, got %v", ids)
	}
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	script := connHolderScript(t, base)
	// Each holder runs in the background, so the pane takes the next one.
	run := func(window, out string, n, forks int) {
		t.Helper()
		line := "python3 " + script + " " + sock + " " + out + " " + strconv.Itoa(n) + " " + strconv.Itoa(forks) + " &\n"
		if out, err := tuiosCLI(t, base, "send-text", "-s", "conns", "-w", window, line); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}

	// One pane cannot hold more than its share.
	first := filepath.Join(base, "first")
	run(ids[0], first, 100, 0)
	got := heldCount(t, first, "main")
	tr.add("pane 1 asked for 100 connections and holds %d", got)
	if got != 64 {
		t.Errorf("ASSERTION: one pane holds %d connections, want 64", got)
	}

	// Another pane still gets what it asks for.
	second := filepath.Join(base, "second")
	run(ids[1], second, 10, 0)
	got = heldCount(t, second, "main")
	tr.add("pane 2 asked for 10 connections and holds %d", got)
	if got != 10 {
		t.Errorf("ASSERTION: a second pane holds %d connections, want 10", got)
	}

	// Children with no terminal fill what panes may hold, and no more.
	forked := filepath.Join(base, "forked")
	run(ids[1], forked, 64, 16)
	total := 64 + 10
	for i := range 16 {
		total += heldCount(t, forked, "c"+strconv.Itoa(i))
	}
	tr.add("panes and their children hold %d connections in all", total)
	if total > 1024-128 {
		t.Errorf("ASSERTION: programs in panes hold %d connections, want at most %d", total, 1024-128)
	}

	// The person's commands still work.
	for _, args := range [][]string{{"ls"}, {"list-windows", "-s", "conns"}} {
		out, err := tuiosCLI(t, base, args...)
		tr.add("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		if err != nil {
			t.Errorf("ASSERTION: tuios %s was refused while panes hold their connections: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	tr.save(t, "pane-conns.txt")
}
