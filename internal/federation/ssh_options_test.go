package federation

import (
	"os/exec"
	"slices"
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
	// Options that write a chosen line into a chosen file: known_hosts at
	// ~/.bashrc, with a host key alias that is a shell line.
	{"-o", "UserKnownHostsFile=~/.bashrc", "-o", "StrictHostKeyChecking=accept-new", "-o", "HashKnownHosts=no", "-o", "HostKeyAlias=x;{sh,/tmp/p};#"},
	{"-o", "UserKnownHostsFile /dev/null"},
	{"-o", "GlobalKnownHostsFile=/tmp/x"},
	{"-o", "HostKeyAlias=x;{sh,/tmp/p};#"},
	{"-o", "HostKeyAlias x y"},
	{"-o", "HostName=a;b"},
	{"-o", "User=me$(id)"},
	{"-l", "me;id"},
	{"-o", "ControlPath=~/.bashrc"},
	{"-S", "/tmp/sock"},
	{"-L", "/tmp/sock:host:22"},
	{"-E", "/tmp/log"},
	{"-o", "Port=22x"},
	{"-o", "SendEnv A B"},
	{"-o", "ForwardAgent=/run/user/1000/bus"},
	{"-oForwardAgent=/tmp/sock"},
	{"-J", "x,-Fc"},
	{"-J", "a@-Fc"},
	{"-o", "ProxyJump=a@-Fc"},
	{"-o", "ProxyJump=x,-Fc"},
	{"-J", "x,,y"},
}

// acceptedSSHOptions are what a host entry needs ssh_options for.
var acceptedSSHOptions = [][]string{
	nil,
	{"-J", "bastion"},
	{"-J", "me@jump:2222,other"},
	{"-o", "StrictHostKeyChecking=yes", "-p", "2222", "-i", "~/.ssh/id"},
	{"-o", "ProxyJump=bastion"},
	{"-o", "HostKeyAlias=build.example"},
	{"-o", "User=me", "-l", "me", "-o", "HostName=[::1]"},
	{"-4", "-A"},
	{"-o", "ForwardAgent=yes"},
	{"-J", "me@jump:2222,[::1]:22,other"},
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

// TestADashCommandIsNeverAnSSHOption: ssh reads options after the host name
// as well, so a host command of -oProxyCommand=... would run a command here.
// Every ssh argv ends ssh's options with -- before the host, and a command or
// addr that starts with a dash is refused.
//
// Negative control: without the -- in linkArgs, ssh -G reads the command as
// ProxyCommand.
func TestADashCommandIsNeverAnSSHOption(t *testing.T) {
	bad := Host{Name: "x", Addr: "example.invalid", Command: "-oProxyCommand=touch /tmp/x"}
	if table, problems := NewTable([]Host{bad}); len(problems) != 1 {
		t.Errorf("a host whose command starts with a dash was kept: %v", table.Names())
	}
	link := linkArgs(bad)
	open, err := bad.OpenArgs("new")
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{"link": link, "open": open} {
		i := slices.Index(args, "example.invalid")
		if i < 1 || args[i-1] != "--" {
			t.Errorf("the %s argv does not end ssh's options before the host: %q", name, args)
		}
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return
	}
	for name, args := range map[string][]string{"link": link, "open": open} {
		out, err := exec.Command("ssh", append([]string{"-G", "-F", "/dev/null"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ssh -G with the %s argv: %v\n%s", name, err, out)
		}
		if strings.Contains(strings.ToLower(string(out)), "proxycommand touch") {
			t.Errorf("ssh reads the %s argv's command as ProxyCommand", name)
		}
	}
}
