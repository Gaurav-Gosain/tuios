package session

import "testing"

// foregroundOf is one window's entry of list-windows with foreground set.
func foregroundOf(t *testing.T, c *verbConn, session, window string) map[string]any {
	t.Helper()
	res := result(t, callP(c, t, "list-windows", map[string]any{"session": session, "foreground": true}))
	windows, _ := res["windows"].([]any)
	for _, raw := range windows {
		if w, _ := raw.(map[string]any); w["window_id"] == window {
			return w
		}
	}
	t.Fatalf("list-windows lists no window %s", window)
	return nil
}

// processNamed is the entry of foreground_processes whose name is name.
func processNamed(entry map[string]any, name string) map[string]any {
	procs, _ := entry["foreground_processes"].([]any)
	for _, raw := range procs {
		if p, _ := raw.(map[string]any); p["name"] == name {
			return p
		}
	}
	return nil
}

func skipWithoutForegroundRead(t *testing.T, sess *Session, window string) *PTY {
	t.Helper()
	pty := sess.GetPTY(ptyOf(t, sess, window))
	if pty == nil || pty.ShellPID() <= 0 {
		t.Fatal("the window has no shell")
	}
	if _, ok := readForegroundPGID(pty.ShellPID()); !ok {
		t.Skip("this platform does not say which process group holds a terminal")
	}
	return pty
}

// TestForegroundArgumentsNeedMoreThanRead: a pane that may only read another
// gets the pids and names of its processes, and no arguments or directory.
// It gets its own in full.
func TestForegroundArgumentsNeedMoreThanRead(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp, reader, other, _ := scopeFixture(t)
	setStrict(d, "read")
	sess := d.manager.GetSession("a")
	skipWithoutForegroundRead(t, sess, reader)
	otherPTY := skipWithoutForegroundRead(t, sess, other)
	waitPrompt(t, "the shell to take the terminal", func() bool { return shellAtPrompt(otherPTY) })
	if _, err := otherPTY.Write([]byte("sleep 4321\r")); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, "sleep to take the foreground", func() bool { return !shellAtPrompt(otherPTY) })

	d.setApprovalPeer(func(*connState) (bool, string) { return true, reader })
	c := dialVerb(t, sp)
	for verb, entry := range map[string]map[string]any{
		"list-windows": foregroundOf(t, c, "a", other),
		"get-window":   result(t, callP(c, t, "get-window", map[string]any{"session": "a", "window": other})),
	} {
		p := processNamed(entry, "sleep")
		if p == nil {
			t.Fatalf("%s names no sleep for a pane with read: %v", verb, entry["foreground_processes"])
		}
		for _, field := range []string{"argv", "argv0", "cmdline", "cwd"} {
			if _, has := p[field]; has {
				t.Errorf("%s gave a read-only pane another pane's %s: %v", verb, field, p[field])
			}
		}
	}
	own := foregroundOf(t, c, "a", reader)
	procs, _ := own["foreground_processes"].([]any)
	if len(procs) == 0 {
		t.Fatalf("the pane's own entry names no process: %v", own)
	}
	if argv, _ := procs[0].(map[string]any)["argv"].([]any); len(argv) == 0 {
		t.Errorf("the pane's own process has no argv: %v", procs[0])
	}
}
