package federation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Address discovery.
//
// Every test here feeds a fixture. Nothing in this package reads the developer's
// own ~/.ssh/config, and the one function that would is given a path.

const sshConfigFixture = `# A comment about the file
Host buildbox
  HostName 10.0.0.4
  User gaurav
  IdentityFile ~/.ssh/id_ed25519

Host lab-01 lab-02
  User lab

Host *.internal
  ProxyJump bastion

Host *
  ServerAliveInterval 30

Host=equals-form
  User someone

Host "quoted-name"
  User someone

Include ~/.ssh/config.d/*
`

// TestSSHAliasesAreTheHostNames reads the exact list, so it also proves the
// patterns ("*", "*.internal") are not offered as addresses and that the
// values under a Host block (a machine, a user, a key file) are never read:
// any keyword but Host reaching the list fails it.
func TestSSHAliasesAreTheHostNames(t *testing.T) {
	got := SSHConfigAliases(strings.NewReader(sshConfigFixture))
	want := []string{"buildbox", "equals-form", "lab-01", "lab-02", "quoted-name"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ASSERTION: the aliases read from the ssh config are %v, wanted %v", got, want)
	}
}

// Include is not followed. The file the user edits is the file that is read.
func TestSSHAliasesDoNotFollowInclude(t *testing.T) {
	dir := t.TempDir()
	included := filepath.Join(dir, "extra")
	if err := os.WriteFile(included, []byte("Host from-the-include\n"), 0o600); err != nil {
		t.Fatalf("write the included file: %v", err)
	}
	main := filepath.Join(dir, "config")
	body := "Include " + included + "\nHost here\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatalf("write the ssh config: %v", err)
	}
	got := ReadSSHAliases(main)
	if strings.Join(got, ",") != "here" {
		t.Errorf("ASSERTION: an Include was followed, got %v", got)
	}
}
