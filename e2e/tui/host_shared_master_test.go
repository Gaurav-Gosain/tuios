package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
		name   string
		mode   os.FileMode
		rides  bool
		config string
	}{
		{"a master folder of the user's own", 0o700, true, ""},
		{"no master folder", 0, false, ""},
		{"a master folder others can write", 0o777, false, ""},
		{"the user's ssh config shares connections itself", 0o700, false, "'controlpath /home/someone/.ssh/cm-%C'"},
		{"the host forwards the agent", 0o700, false, "'forwardagent yes'"},
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
				if err := os.Chmod(cm, c.mode); err != nil {
					t.Fatalf("chmod the master folder: %v", err)
				}
			}
			want := "ControlPath=" + filepath.Join(cm, "%C")

			if out, err := tuiosCLI(t, remote, "start-server"); err != nil {
				t.Fatalf("start the far daemon: %v\n%s", err, out)
			}
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

			listing := waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "│ up ") }, "the link to build comes up")
			t.Logf("hosts:\n%s", listing)

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
