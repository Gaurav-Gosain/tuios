package federation

import (
	"os/exec"
	"strings"
	"testing"
)

// refusedSSHOptions make ssh run a command, or are spelled in a way ssh reads
// differently from plain text. Every spelling ssh reads as ProxyCommand is
// here: a quoted keyword, and keywords split from the value by CR or LF.
var refusedSSHOptions = [][]string{
	{"-o", "ProxyCommand=touch /tmp/x"},
	{"-o", "proxycommand touch /tmp/x"},
	{"-oProxyCommand=touch /tmp/x"},
	{"-vo", "PROXYCOMMAND=sh"},
	{"-o", `"ProxyCommand" touch /tmp/x`},
	{"-o", `ProxyCommand"" touch /tmp/x`},
	{"-o", "ProxyCommand\rtouch /tmp/x"},
	{"-o", "ProxyCommand\ntouch /tmp/x"},
	{"-o", "ProxyCommand\ftouch /tmp/x"},
	{"-o", " ProxyCommand touch /tmp/x"},
	{"-o", "\tProxyCommand touch /tmp/x"},
	{"-o", "Port 22\nProxyCommand touch /tmp/x"},
	{"-o", "LocalCommand=sh", "-o", "PermitLocalCommand=yes"},
	{"-o", "KnownHostsCommand=sh"},
	{"-o", "PKCS11Provider=/tmp/x.so"},
	{"-o", "SecurityKeyProvider=/tmp/x.so"},
	{"-o", "Include=/tmp/cfg"},
	{"-F", "/tmp/cfg"},
	{"-F/tmp/cfg"},
	{"-I", "/tmp/x.so"},
	{"-J", "-oProxyCommand=sh"},
	{"-o", "ProxyJump=-oProxyCommand=sh"},
	{"-i", "key\nProxyCommand"},
	{"host", "touch /tmp/x"},
	{"-o"},
	{"-W", "host:22"},
}

// acceptedSSHOptions are what a host entry needs ssh_options for.
var acceptedSSHOptions = [][]string{
	nil,
	{"-J", "bastion"},
	{"-J", "me@jump:2222,other"},
	{"-o", "StrictHostKeyChecking=yes", "-p", "2222", "-i", "~/.ssh/id"},
	{"-o", "ProxyJump=bastion"},
	{"-o", "UserKnownHostsFile /dev/null"},
	{"-4", "-A"},
	{"-vo", "ServerAliveInterval=5"},
}

// TestSSHOptionsThatRunCommandsAreRefused: a host entry in config.toml, which
// a process in a pane can write, must not make the daemon's ssh run a
// command. Only safe options written plainly are accepted, and a host with
// any other is dropped.
//
// Negative control: with CheckSSHOptions cut from NewTable, the host with
// ProxyCommand is kept.
func TestSSHOptionsThatRunCommandsAreRefused(t *testing.T) {
	for _, opts := range refusedSSHOptions {
		if err := CheckSSHOptions(opts); err == nil {
			t.Errorf("ssh_options %q was accepted", opts)
		}
		table, problems := NewTable([]Host{{Name: "build", Addr: "buildbox", SSHOptions: opts}})
		if _, err := table.Lookup("build"); err == nil || len(problems) != 1 {
			t.Errorf("a host with ssh_options %q was kept (problems %v)", opts, problems)
		}
	}
	for _, opts := range acceptedSSHOptions {
		if err := CheckSSHOptions(opts); err != nil {
			t.Errorf("ssh_options %q was refused: %v", opts, err)
		}
	}
}

// sshG runs ssh -G with opts and returns what ssh resolves, lower case.
func sshG(t *testing.T, opts []string) (string, bool) {
	t.Helper()
	args := append(append([]string{"-G", "-F", "/dev/null"}, opts...), "example.invalid")
	out, err := exec.Command("ssh", args...).CombinedOutput()
	return strings.ToLower(string(out)), err == nil
}

// TestSSHReadsTheRefusedSpellingsAsCommands confirms the spellings above
// against ssh's own reading: each quoted or split ProxyCommand spelling is one
// ssh reads as ProxyCommand, and no accepted list sets a command.
func TestSSHReadsTheRefusedSpellingsAsCommands(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh is not installed")
	}
	for _, opts := range [][]string{
		{"-o", `"ProxyCommand" touch /tmp/x`},
		{"-o", `ProxyCommand"" touch /tmp/x`},
		{"-o", "ProxyCommand\rtouch /tmp/x"},
		{"-o", "ProxyCommand\ntouch /tmp/x"},
		{"-o", " ProxyCommand touch /tmp/x"},
		{"-o", "\tProxyCommand touch /tmp/x"},
	} {
		out, ok := sshG(t, opts)
		if !ok || !strings.Contains(out, "proxycommand touch /tmp/x") {
			t.Errorf("ssh does not read %q as ProxyCommand, so the test spelling is wrong:\n%s", opts, out)
		}
	}
	// The command-running keys as ssh sets them with no options at all.
	commandKeys := func(out string) map[string]string {
		keys := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			for _, key := range []string{"proxycommand", "localcommand", "permitlocalcommand", "knownhostscommand", "pkcs11provider", "securitykeyprovider"} {
				if strings.HasPrefix(line, key+" ") {
					keys[key] = line
				}
			}
		}
		return keys
	}
	base, _ := sshG(t, nil)
	want := commandKeys(base)
	for _, opts := range acceptedSSHOptions {
		out, ok := sshG(t, opts)
		if !ok {
			t.Errorf("ssh does not accept %q:\n%s", opts, out)
			continue
		}
		for key, line := range commandKeys(out) {
			if want[key] != line {
				t.Errorf("ssh reads %q as setting %s", opts, line)
			}
		}
	}
}
