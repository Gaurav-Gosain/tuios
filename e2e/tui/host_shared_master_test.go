package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A link that rides the ssh master a person opened.
//
// The daemon's links run ssh with BatchMode, so they can never answer a
// passphrase, password or code prompt. A client that can ask the person
// (tuios-gpui) opens the first connection itself, as an ssh master in
// $XDG_RUNTIME_DIR/tuios/cm, and the daemon's ssh must use that master.
// ssh names the master's socket by %C, so the daemon only has to pass the
// same ControlPath, with ControlMaster=no so that it never opens a master of
// its own and never fails for want of one.
//
// The ssh stand-in records every argv it is run with, then reaches the far
// daemon as the other host tests do. What would pass a weaker test and fail
// this one: options added to the CLI's ssh and not the daemon's link (the
// link's argv has no ControlPath), a ControlMaster=auto that opens a master
// in the daemon (the argv says auto), or a folder others can write that is
// trusted (the third case's argv has a ControlPath), or a ControlPath of the
// person's own ssh config that the link no longer uses (the fourth case's
// argv names the folder).

// writeArgvSSH is writeFakeSSHTo that first appends its argv, one line per
// run, to log. Asked for its options (-G), it prints config: the lines a
// person's ~/.ssh/config would give, such as a ControlPath of their own.
func writeArgvSSH(t *testing.T, dir, remoteBase, log, config string) string {
	t.Helper()
	inner := writeFakeSSHTo(t, dir, remoteBase)
	path := filepath.Join(dir, "fake-ssh-argv")
	body := "#!/bin/sh\nif [ \"$1\" = -G ]; then printf '%s\\n' 'controlmaster false' " + config + "; exit 0; fi\n" +
		"printf '%s\\n' \"$*\" >>'" + log + "'\nexec '" + inner + "' \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

// linkArgv waits until the daemon's link has run ssh for the proxy, and
// returns that argv line.
func linkArgv(t *testing.T, log string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(log)
		for line := range strings.SplitSeq(string(b), "\n") {
			if strings.Contains(line, "stdio-proxy") {
				return line
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(log)
	t.Fatalf("the daemon never ran ssh for its link to build:\n%s", b)
	return ""
}

func TestLinkRidesTheSharedSSHMaster(t *testing.T) {
	cases := []struct {
		name string
		// mode is the master folder's mode, 0 for no folder.
		mode  os.FileMode
		rides bool
		// config is what ssh -G adds, as quoted shell words.
		config string
	}{
		{"a master folder of the user's own", 0o700, true, ""},
		{"no master folder", 0, false, ""},
		{"a master folder others can write", 0o777, false, ""},
		{"the user's ssh config shares connections itself", 0o700, false, "'controlpath /home/someone/.ssh/cm-%C'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			remote := remoteMachine(t)
			log := filepath.Join(base, "ssh-argv.log")
			ssh := writeArgvSSH(t, base, remote, log, c.config)
			writeOneHostConfig(t, base, tuiosBin)
			env := []string{"TUIOS_SSH=" + ssh}
			cm := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "cm")
			if c.mode != 0 {
				if err := os.MkdirAll(cm, 0o700); err != nil {
					t.Fatalf("make the master folder: %v", err)
				}
				// MkdirAll applies the umask; the mode under test is set
				// exactly.
				if err := os.Chmod(cm, c.mode); err != nil {
					t.Fatalf("chmod the master folder: %v", err)
				}
			}
			want := "ControlPath=" + filepath.Join(cm, "%C")

			// The far machine runs its daemon, so the link can come up.
			if out, err := tuiosCLI(t, remote, "start-server"); err != nil {
				t.Fatalf("start the far daemon: %v\n%s", err, out)
			}
			// The daemon's link: the daemon dials it at start.
			killDaemon(t, base)
			if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
				t.Fatalf("start-server: %v\n%s", err, out)
			}
			argv := linkArgv(t, log)
			t.Logf("the link's ssh: %s", argv)
			if got := strings.Contains(argv, want); got != c.rides {
				t.Fatalf("ASSERTION: the link's ssh names the shared master: %v, want %v\nargv: %s", got, c.rides, argv)
			}
			if c.rides && !strings.Contains(argv, "ControlMaster=no") {
				t.Fatalf("ASSERTION: the link may open a master of its own:\n%s", argv)
			}
			if !strings.Contains(argv, "BatchMode=yes") {
				t.Fatalf("ASSERTION: the link's ssh can prompt:\n%s", argv)
			}

			// The link still comes up: with no live master at the path, ssh
			// connects as it always did.
			listing := waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "│ up ") }, "the link to build comes up")
			t.Logf("hosts:\n%s", listing)

			// tuios hosts test dials in the CLI with the same options.
			_ = os.Remove(log)
			if out, err := tuiosCLIEnv(t, base, env, "hosts", "test", "build"); err != nil {
				t.Fatalf("hosts test: %v\n%s", err, out)
			}
			b, _ := os.ReadFile(log)
			if got := strings.Contains(string(b), want); got != c.rides {
				t.Fatalf("ASSERTION: tuios hosts test names the shared master: %v, want %v\n%s", got, c.rides, b)
			}
		})
	}
}
