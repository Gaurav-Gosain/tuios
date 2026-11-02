package app

import (
	"os"
	"strings"
	"testing"
)

// TestHostTitleNeverReadsARemoteShellLocally is a security boundary. A client
// attached to a session on another machine holds each pane's shell pid as a
// pid over there. Reading that pid on this machine reads some unrelated local
// process, and {cwd} would put its directory in the host terminal's title.
// The pane's directory is the one the daemon reported, and only that.
//
// This test process stands in for the unrelated local program: its pid is the
// one the client holds, and its working directory must not show.
func TestHostTitleNeverReadsARemoteShellLocally(t *testing.T) {
	local, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	w, _ := layoutWindow(t, "remote-win")
	w.Workspace = 1
	w.ShellPgid = os.Getpid()
	m := layoutOS(w)
	m.SessionName = "far"
	m.Settings.HostTitle = "{cwd}"

	m.AttachedHost = "build"
	if got := m.hostTitle(); strings.Contains(got, local) {
		t.Fatalf("attached to another machine, {cwd} read a local process: %q", got)
	}

	w.Cwd = "/srv/app"
	if got := m.hostTitle(); got != "/srv/app" {
		t.Fatalf("attached to another machine, {cwd} = %q, want the daemon's /srv/app", got)
	}
}
