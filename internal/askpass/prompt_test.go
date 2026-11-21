package askpass

import (
	"os"
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

func TestKnownProgram(t *testing.T) {
	for path, want := range map[string]bool{
		"/usr/bin/ssh":                       true,
		"/usr/lib/git-core/git-remote-https": true,
		"/usr/bin/gpg":                       true,
		"/tmp/fish":                          false,
		"/usr/bin/python3":                   false,
	} {
		if got := KnownProgram(path); got != want {
			t.Errorf("KnownProgram(%q) = %v, want %v", path, got, want)
		}
	}
}
