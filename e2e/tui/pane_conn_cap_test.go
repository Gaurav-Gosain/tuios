package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

func heldCount(t *testing.T, out, tag string) int {
	t.Helper()
	text := waitFileHas(t, out+"."+tag, "the holder "+tag+" never wrote its count", 60*time.Second, func(s string) bool { return s != "" })
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		t.Fatalf("holder %s wrote %q", tag, text)
	}
	return n
}

// TestPanesCannotTakeThePersonsConnections runs holders in panes of a real
// daemon: one pane asks for 100 connections, then four of its children in
// sessions of their own for 64 each, a second pane for 10, and 14 more panes
// for 64 each. The person's tuios commands must still work.
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
	run := func(window, out string, n, forks int) {
		t.Helper()
		line := "python3 " + script + " " + sock + " " + out + " " + strconv.Itoa(n) + " " + strconv.Itoa(forks) + " &\n"
		if out, err := tuiosCLI(t, base, "send-text", "-s", "conns", "-w", window, line); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}

	first := filepath.Join(base, "first")
	run(ids[0], first, 100, 0)
	got := heldCount(t, first, "main")
	tr.add("pane 1 asked for 100 connections and holds %d", got)
	if got != 64 {
		t.Errorf("ASSERTION: one pane holds %d connections, want 64", got)
	}

	escaped := filepath.Join(base, "escaped")
	run(ids[0], escaped, 64, 4)
	for i := range 4 {
		n := heldCount(t, escaped, "c"+strconv.Itoa(i))
		tr.add("setsid child %d of pane 1 asked for 64 connections and holds %d", i, n)
		if n != 0 {
			t.Errorf("ASSERTION: a setsid child of a full pane holds %d connections, want 0", n)
		}
	}

	second := filepath.Join(base, "second")
	run(ids[1], second, 10, 0)
	got = heldCount(t, second, "main")
	tr.add("pane 2 asked for 10 connections and holds %d", got)
	if got != 10 {
		t.Errorf("ASSERTION: a second pane holds %d connections, want 10", got)
	}

	for i := range 14 {
		name := "more" + strconv.Itoa(i)
		if o, err := tuiosCLI(t, base, "new-window", "-s", "conns", name, "--no-focus"); err != nil {
			t.Fatalf("open pane %s: %v\n%s", name, err, o)
		}
	}
	all := grantWindowIDs(t, base, "conns")
	if len(all) != 16 {
		t.Fatalf("want 16 panes, got %d", len(all))
	}
	more := filepath.Join(base, "more")
	for i, id := range all {
		if id != ids[0] && id != ids[1] {
			run(id, more+strconv.Itoa(i), 64, 0)
		}
	}
	total := 64 + 10
	for i, id := range all {
		if id != ids[0] && id != ids[1] {
			total += heldCount(t, more+strconv.Itoa(i), "main")
		}
	}
	tr.add("16 panes hold %d connections in all", total)
	if total > 1024-128 {
		t.Errorf("ASSERTION: programs in panes hold %d connections, want at most %d", total, 1024-128)
	}

	for _, args := range [][]string{{"ls"}, {"list-windows", "-s", "conns"}} {
		out, err := tuiosCLI(t, base, args...)
		tr.add("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		if err != nil {
			t.Errorf("ASSERTION: tuios %s was refused while panes hold their connections: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	tr.save(t, "pane-conns.txt")
}
