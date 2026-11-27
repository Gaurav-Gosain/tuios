package session

import (
	"slices"
	"testing"
)

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

// TestForegroundStateTellsAPromptFromAProgram: with no client attached and no
// shell integration, list-windows and get-window say which panes sit at their
// prompt and which run something, and name what runs with its arguments.
func TestForegroundStateTellsAPromptFromAProgram(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp, idle, busy, _ := scopeFixture(t)
	c := dialVerb(t, sp)
	sess := d.manager.GetSession("a")
	idlePTY, busyPTY := skipWithoutForegroundRead(t, sess, idle), skipWithoutForegroundRead(t, sess, busy)
	waitPrompt(t, "both shells to take their terminals", func() bool { return shellAtPrompt(idlePTY) && shellAtPrompt(busyPTY) })

	if _, err := busyPTY.Write([]byte("sleep 4321\r")); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, "sleep to take the foreground", func() bool { return !shellAtPrompt(busyPTY) })

	if got := foregroundOf(t, c, "a", idle); got["foreground_state"] != foregroundShell {
		t.Errorf("the pane at its prompt: foreground_state %v, want %s", got["foreground_state"], foregroundShell)
	}
	listed := foregroundOf(t, c, "a", busy)
	got := result(t, callP(c, t, "get-window", map[string]any{"session": "a", "window": busy}))
	for verb, entry := range map[string]map[string]any{"list-windows": listed, "get-window": got} {
		if entry["foreground_state"] != foregroundProgramState {
			t.Errorf("%s, the pane running sleep: foreground_state %v, want %s", verb, entry["foreground_state"], foregroundProgramState)
		}
		p := processNamed(entry, "sleep")
		if p == nil {
			t.Fatalf("%s names no sleep in the foreground: %v", verb, entry["foreground_processes"])
		}
		if pid, _ := p["pid"].(float64); int(pid) != int(entry["foreground_process_group_id"].(float64)) {
			t.Errorf("%s: sleep has pid %v, the foreground group is %v", verb, p["pid"], entry["foreground_process_group_id"])
		}
		if argv, _ := p["argv"].([]any); !slices.Equal(argv, []any{"sleep", "4321"}) {
			t.Errorf("%s: sleep's argv is %v", verb, p["argv"])
		}
		if cwd, _ := p["cwd"].(string); cwd == "" {
			t.Errorf("%s: sleep has no cwd", verb)
		}
	}

	// The read is asked for: a plain list-windows makes none.
	plain := result(t, callP(c, t, "list-windows", map[string]any{"session": "a"}))
	for _, raw := range plain["windows"].([]any) {
		if _, has := raw.(map[string]any)["foreground_state"]; has {
			t.Fatal("list-windows read the foreground without being asked")
		}
	}
}

// TestForegroundStateOfAPaneThatRunsNoShell: a pane started with a program
// leads its terminal's foreground group itself, as a shell at its prompt
// does. It is not at a prompt.
func TestForegroundStateOfAPaneThatRunsNoShell(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	sess := makeSessionWithWindow(t, d, "direct")
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "sleeper", Command: []string{"sleep", "4321"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pty := skipWithoutForegroundRead(t, sess, win.ID)
	waitPrompt(t, "sleep to hold its terminal", func() bool { return shellAtPrompt(pty) })

	got := foregroundOf(t, c, "direct", win.ID)
	if got["foreground_state"] != foregroundProgramState || processNamed(got, "sleep") == nil {
		t.Fatalf("a pane that runs sleep and no shell: foreground_state %v, processes %v", got["foreground_state"], got["foreground_processes"])
	}
}

// TestForegroundStateIsUnknownWhenTheKernelDoesNotSay: a read that fails is
// not an idle pane.
func TestForegroundStateIsUnknownWhenTheKernelDoesNotSay(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	// Set before the daemon starts and put back after it stops, so no
	// daemon goroutine reads the variable while it changes.
	real := foregroundPGID
	t.Cleanup(func() { foregroundPGID = real })
	foregroundPGID = func(int) (int, bool) { return 0, false }
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	sess := makeSessionWithWindow(t, d, "unknownfg")
	win := sess.GetState().Windows[0]
	if pty := sess.GetPTY(win.PTYID); pty == nil || pty.ShellPID() <= 0 {
		t.Fatal("the window has no shell")
	}
	for verb, entry := range map[string]map[string]any{
		"list-windows": foregroundOf(t, c, "unknownfg", win.ID),
		"get-window":   result(t, callP(c, t, "get-window", map[string]any{"session": "unknownfg", "window": win.ID})),
	} {
		if entry["foreground_state"] != foregroundUnknown {
			t.Errorf("%s: foreground_state %v, want %s", verb, entry["foreground_state"], foregroundUnknown)
		}
		if _, has := entry["foreground_processes"]; has {
			t.Errorf("%s names processes it could not read: %v", verb, entry["foreground_processes"])
		}
	}
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
