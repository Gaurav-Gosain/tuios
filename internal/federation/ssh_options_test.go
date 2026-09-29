package federation

import (
	"strings"
	"testing"
)

// TestSSHOptionsThatRunCommandsAreRefused: a host entry in config.toml, which
// a process in a pane can write, must not make the daemon's ssh run a
// command. Every spelling ssh accepts is refused, and the host is dropped.
//
// Negative control: with CheckSSHOptions cut from NewTable, the host with
// ProxyCommand is kept.
func TestSSHOptionsThatRunCommandsAreRefused(t *testing.T) {
	for _, opts := range [][]string{
		{"-o", "ProxyCommand=touch /tmp/x"},
		{"-o", "proxycommand touch /tmp/x"},
		{"-oProxyCommand=touch /tmp/x"},
		{"-vo", "PROXYCOMMAND=sh"},
		{"-o", "LocalCommand=sh", "-o", "PermitLocalCommand=yes"},
		{"-o", "KnownHostsCommand=sh"},
		{"-o", "PKCS11Provider=/tmp/x.so"},
		{"-o", "SecurityKeyProvider=/tmp/x.so"},
		{"-F", "/tmp/cfg"},
		{"-F/tmp/cfg"},
		{"-I", "/tmp/x.so"},
		{"-J", "-oProxyCommand=sh"},
		{"-o", "ProxyJump=-oProxyCommand=sh"},
		{"host", "touch /tmp/x"},
		{"-o"},
	} {
		if err := CheckSSHOptions(opts); err == nil {
			t.Errorf("ssh_options %q was accepted", opts)
		}
		table, problems := NewTable([]Host{{Name: "build", Addr: "buildbox", SSHOptions: opts}})
		if _, err := table.Lookup("build"); err == nil || len(problems) != 1 {
			t.Errorf("a host with ssh_options %q was kept (problems %v)", opts, problems)
		}
	}
	for _, opts := range [][]string{
		nil,
		{"-J", "bastion"},
		{"-J", "me@jump:2222,other"},
		{"-o", "StrictHostKeyChecking=yes", "-p", "2222", "-i", "~/.ssh/id"},
		{"-o", "ProxyJump=bastion"},
		{"-4", "-A"},
	} {
		if err := CheckSSHOptions(opts); err != nil {
			t.Errorf("ssh_options %q was refused: %v", opts, err)
		}
	}
	if err := CheckSSHOptions([]string{"-o", "ProxyCommand=x"}); !strings.Contains(err.Error(), "proxycommand") {
		t.Errorf("the refusal %q does not name the option", err)
	}
}
