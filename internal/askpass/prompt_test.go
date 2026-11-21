package askpass

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := []struct {
		prompt, kind, subject string
	}{
		{"Enter passphrase for key '" + home + "/.ssh/id_ed25519': ", KindPassphrase, "~/.ssh/id_ed25519"},
		{"Enter passphrase for /tmp/k:", KindPassphrase, "/tmp/k"},
		{"git@github.com's password: ", KindPassword, "git@github.com"},
		{"Password for 'https://u@example.com': ", KindPassword, "https://u@example.com"},
		{"Enter PIN for ECDSA-SK key /k: ", KindPIN, "/k"},
		{"Enter the code: ", KindSecret, ""},
		{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", KindConfirm, ""},
		{"Username for 'https://github.com': ", KindUsername, "https://github.com"},
		// A word that holds pin is not a PIN.
		{"Spinning up, type the token:", KindSecret, ""},
	}
	for _, c := range cases {
		got := Read(c.prompt)
		if got.Kind != c.kind || got.Subject != c.subject {
			t.Errorf("Read(%q) = kind %q subject %q, want %q %q", c.prompt, got.Kind, got.Subject, c.kind, c.subject)
		}
	}
}

// TestKnownProgram: a program counts as known by where it lives, not by its
// name. A copy of a binary named ssh in a folder a pane can write is not ssh.
func TestKnownProgram(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if KnownProgram(fake) {
		t.Errorf("a file named ssh in a temporary folder counts as ssh")
	}
	if KnownProgram("/usr/bin/python3") {
		t.Errorf("python3 counts as a program that asks for passphrases")
	}
	if !KnownProgram("/usr/bin/ssh") {
		t.Errorf("/usr/bin/ssh does not count as ssh")
	}
	if real, err := exec.LookPath("ssh-keygen"); err == nil {
		if real, err = filepath.EvalSymlinks(real); err == nil && !KnownProgram(real) {
			t.Errorf("the ssh-keygen PATH finds (%s) does not count", real)
		}
	}
}

func TestMaskCommandLine(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"git", "push", "https://bob:ghp_SECRET@github.com/o/r.git"}, "git push https://bob:***@github.com/o/r.git"},
		{[]string{"git", "clone", "https://ghp_SECRET@github.com/o/r"}, "git clone https://***@github.com/o/r"},
		{[]string{"sshpass", "-p", "SECRET", "ssh", "-p", "2222", "host"}, "sshpass -p *** ssh -p 2222 host"},
		{[]string{"tool", "--token", "SECRET", "--password=SECRET", "x"}, "tool --token *** --password=*** x"},
		{[]string{"ssh", "-p", "2222", "git@github.com"}, "ssh -p 2222 git@github.com"},
		{[]string{"git", "fetch", "ssh://git@host:22/r"}, "git fetch ssh://git@host:22/r"},
		{[]string{"git", "-c", "remote.o.url=https://ghp_SECRET@h/r", "fetch"}, "git -c remote.o.url=https://***@h/r fetch"},
	}
	for _, c := range cases {
		if got := MaskCommandLine(c.args); got != c.want {
			t.Errorf("MaskCommandLine(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}
