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

func TestSSHAliasesAreTheHostNames(t *testing.T) {
	got := SSHConfigAliases(strings.NewReader(sshConfigFixture))
	want := []string{"buildbox", "equals-form", "lab-01", "lab-02", "quoted-name"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ASSERTION: the aliases read from the ssh config are %v, wanted %v", got, want)
	}
}

func TestSSHAliasesDropPatterns(t *testing.T) {
	got := strings.Join(SSHConfigAliases(strings.NewReader(sshConfigFixture)), ",")
	for _, pattern := range []string{"*", "*.internal"} {
		if strings.Contains(got, pattern) {
			t.Errorf("ASSERTION: the pattern %q was offered as an address, got %s", pattern, got)
		}
	}
}

// The values under a Host block name a machine, a user and a key file. None of
// them is read, and the test that proves it is the one that would fail if a
// future change started reading any keyword but Host.
func TestSSHAliasesReadNothingButHostNames(t *testing.T) {
	got := strings.Join(SSHConfigAliases(strings.NewReader(sshConfigFixture)), " ")
	for _, secret := range []string{"10.0.0.4", "gaurav", "id_ed25519", "bastion", "30"} {
		if strings.Contains(got, secret) {
			t.Errorf("ASSERTION: a value that is not a Host name reached the list: %q in %q", secret, got)
		}
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

func TestReadSSHAliasesOnAMissingFileIsEmpty(t *testing.T) {
	if got := ReadSSHAliases(filepath.Join(t.TempDir(), "nothing")); len(got) != 0 {
		t.Errorf("ASSERTION: a missing ssh config produced aliases, got %v", got)
	}
	if got := ReadSSHAliases(""); len(got) != 0 {
		t.Errorf("ASSERTION: an empty path produced aliases, got %v", got)
	}
}

func TestSSHAliasesAreBounded(t *testing.T) {
	var b strings.Builder
	for i := range maxSSHAliases * 2 {
		b.WriteString("Host machine-")
		b.WriteString(strings.Repeat("0", 4-len(itoa(i))))
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	got := SSHConfigAliases(strings.NewReader(b.String()))
	if len(got) > maxSSHAliases {
		t.Errorf("ASSERTION: the alias list is not bounded, got %d", len(got))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestValidHostNameRefusesTheReservedName(t *testing.T) {
	if err := ValidHostName(LocalHostName); err == nil {
		t.Errorf("ASSERTION: %q was accepted as a host name", LocalHostName)
	}
	if err := ValidHostName("two words"); err == nil {
		t.Error("ASSERTION: a name with a space was accepted")
	}
	if err := ValidHostName("build-box.1"); err != nil {
		t.Errorf("ASSERTION: an ordinary name was refused: %v", err)
	}
}
