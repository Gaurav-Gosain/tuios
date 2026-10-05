package session

import (
	"slices"
	"testing"
)

// The argv an ssh split replays is a security boundary: it is a command line
// read from another process and run again by the daemon. The ways it can go
// wrong, and the cases below that pin each one:
//
//   - The program is taken from the process, so a ./ssh or a renamed binary
//     runs. Every case expects the binary lookPath names.
//   - An option that runs code on this machine is replayed (-F, -I,
//     ProxyCommand and the other -o keywords, mosh --ssh).
//   - The remote command is replayed, so the split runs it a second time.
//   - An option that changes what the session is (-N, -f, -W, -s, -T, or a
//     RemoteCommand -o) is replayed, so the split is not a shell.
//   - A forward is replayed, so the split fails to bind or doubles a tunnel.
//   - An option value is mistaken for the destination, or the destination for
//     an option value, so the split connects somewhere else. An empty
//     argument is how that shift happens.
//   - An unknown option is guessed at, and the rest of the line is misread.
//   - The remote folder breaks out of its quoting in the remote shell.
//   - A mosh-client line that joined arguments with spaces is split wrongly.
func TestParseRemoteLogin(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return "/path/" + name, nil }
	t.Cleanup(func() { lookPath = old })

	const cd = `exec sh -c 'cd "/srv/a b" 2>/dev/null; exec "$SHELL" -l'`
	cases := []struct {
		name string
		argv []string
		dir  string
		want []string // nil: not followed
	}{
		{"plain", []string{"ssh", "host"}, "", []string{"/path/ssh", "host"}},
		{"binary from the process is not used", []string{"./ssh", "host"}, "", []string{"/path/ssh", "host"}},
		{"remote command dropped", []string{"ssh", "-p", "2222", "u@h", "tail", "-f", "log"}, "",
			[]string{"/path/ssh", "-p", "2222", "u@h"}},
		{"options kept", []string{"ssh", "-i", "/k", "-J", "jump", "-o", "User=x", "-l", "me", "h"}, "",
			[]string{"/path/ssh", "-i", "/k", "-J", "jump", "-o", "User=x", "-l", "me", "h"}},
		{"attached values", []string{"ssh", "-p2222", "-oPort=1", "-i/k", "h"}, "",
			[]string{"/path/ssh", "-p", "2222", "-o", "Port=1", "-i", "/k", "h"}},
		{"cluster with value", []string{"ssh", "-4Cp", "22", "h"}, "",
			[]string{"/path/ssh", "-4", "-C", "-p", "22", "h"}},
		{"session changers dropped", []string{"ssh", "-N", "-f", "-T", "-n", "-tt", "-M", "-S", "/s", "h"}, "",
			[]string{"/path/ssh", "-S", "/s", "h"}},
		{"forwards dropped", []string{"ssh", "-L", "8080:x:80", "-R8081:y:81", "-D", "1080", "-W", "x:1", "h"}, "",
			[]string{"/path/ssh", "h"}},
		{"o options dropped", []string{"ssh", "-o", "RemoteCommand=top", "-oSessionType=none", "-o", "RequestTTY no",
			"-o", "ControlMaster=yes", "-o", " controlpersist=10m", "h"}, "", []string{"/path/ssh", "h"}},
		{"options after destination", []string{"ssh", "h", "-p", "2222", "ls"}, "",
			[]string{"/path/ssh", "-p", "2222", "h"}},
		{"double dash before destination", []string{"ssh", "-p", "1", "--", "h", "-p", "2"}, "",
			[]string{"/path/ssh", "-p", "1", "h"}},
		{"double dash after destination", []string{"ssh", "h", "--", "ls"}, "", []string{"/path/ssh", "h"}},
		{"proxy command", []string{"ssh", "-o", "ProxyCommand=nc %h %p", "h"}, "", nil},
		{"proxy command attached", []string{"ssh", "-oproxycommand nc", "h"}, "", nil},
		{"proxy command quoted", []string{"ssh", "-o", ` "ProxyCommand" nc`, "h"}, "", nil},
		{"local command", []string{"ssh", "-o", "PermitLocalCommand=no", "h"}, "", nil},
		{"known hosts command", []string{"ssh", "-oKnownHostsCommand=x", "h"}, "", nil},
		{"pkcs11 provider", []string{"ssh", "-o", "PKCS11Provider=/x.so", "h"}, "", nil},
		{"security key provider", []string{"ssh", "-o", "SecurityKeyProvider=/x.so", "h"}, "", nil},
		{"match", []string{"ssh", "-o", "Match exec true", "h"}, "", nil},
		{"proxy command after a newline", []string{"ssh", "-o", "ProxyCommand\necho PWNED", "h"}, "", nil},
		{"allowed keyword then a newline", []string{"ssh", "-o", "User=x\nProxyCommand=echo PWNED", "h"}, "", nil},
		{"carriage return", []string{"ssh", "-oPort=22\rProxyCommand=x", "h"}, "", nil},
		{"xauth location", []string{"ssh", "-X", "-o", "XAuthLocation=/tmp/evil", "h"}, "", nil},
		{"unknown keyword", []string{"ssh", "-o", "Frobnicate=1", "h"}, "", nil},
		{"include", []string{"ssh", "-o", "Include /tmp/x", "h"}, "", nil},
		{"control character in destination", []string{"ssh", "h\n"}, "", nil},
		{"allowed keywords kept", []string{"ssh", "-X", "-o", "ControlPath=/s", "-o", "ForwardAgent yes", "h"}, "",
			[]string{"/path/ssh", "-X", "-o", "ControlPath=/s", "-o", "ForwardAgent yes", "h"}},
		{"config file", []string{"ssh", "-F", "/x", "h"}, "", nil},
		{"pkcs11 flag", []string{"ssh", "-I", "/x.so", "h"}, "", nil},
		{"print config", []string{"ssh", "-G", "h"}, "", nil},
		{"control command", []string{"ssh", "-O", "exit", "h"}, "", nil},
		{"query", []string{"ssh", "-Q", "cipher"}, "", nil},
		{"empty o value shifts nothing", []string{"ssh", "-o", "", "h"}, "", nil},
		{"empty destination", []string{"ssh", "", "h"}, "", nil},
		{"unknown option", []string{"ssh", "-Z", "h"}, "", nil},
		{"missing value", []string{"ssh", "-p"}, "", nil},
		{"no destination", []string{"ssh", "-v"}, "", nil},
		{"not ssh", []string{"vim", "h"}, "", nil},
		{"sshd is not ssh", []string{"sshd", "-D"}, "", nil},
		{"script", []string{"/bin/sh", "/opt/bin/ssh", "h", "ls"}, "", []string{"/path/ssh", "h"}},
		{"shell running code", []string{"sh", "-c", "x /opt/ssh", "h"}, "", nil},
		{"perl running code", []string{"perl", "-e", "1", "/usr/bin/ssh", "h"}, "", nil},
		{"script with interpreter option", []string{"/usr/bin/perl", "-w", "/usr/bin/mosh", "h"}, "",
			[]string{"/path/mosh", "h"}},
		{"remote folder", []string{"ssh", "-p", "22", "h", "ls"}, "/srv/a b",
			[]string{"/path/ssh", "-p", "22", "-t", "-o", "RemoteCommand=none", "h", cd}},
		{"folder with single quote", []string{"ssh", "h"}, "/x'; rm -rf ~; '", []string{"/path/ssh", "h"}},
		{"folder with double quote", []string{"ssh", "h"}, `/x"y`, []string{"/path/ssh", "h"}},
		{"folder with dollar", []string{"ssh", "h"}, "/x$(id)", []string{"/path/ssh", "h"}},
		{"folder with backslash", []string{"ssh", "h"}, `/x\y`, []string{"/path/ssh", "h"}},
		{"folder with newline", []string{"ssh", "h"}, "/x\nrm", []string{"/path/ssh", "h"}},
		{"relative folder", []string{"ssh", "h"}, "x", []string{"/path/ssh", "h"}},
		{"mosh", []string{"mosh", "-p", "6000", "h", "--", "top"}, "",
			[]string{"/path/mosh", "--port=6000", "h"}},
		{"mosh ssh option", []string{"mosh", "--ssh=ssh -p 2", "h"}, "", nil},
		{"mosh client option", []string{"mosh", "--client", "/x", "h"}, "", nil},
		{"mosh server option", []string{"mosh", "--server=/x", "h"}, "", nil},
		{"mosh folder", []string{"mosh", "h"}, "/srv/a b",
			[]string{"/path/mosh", "h", "--", "sh", "-c", `cd "/srv/a b" 2>/dev/null; exec "$SHELL" -l`}},
		{"mosh unknown option", []string{"mosh", "--frob", "h"}, "", nil},
		{"mosh-client", []string{"mosh-client", "-# -p 6000 u@h |", "100.1.2.3", "60001"}, "",
			[]string{"/path/mosh", "--port=6000", "u@h"}},
		{"mosh-client with a command", []string{"mosh-client", "-# u@h -- top |", "1.2.3.4", "6"}, "", nil},
		{"mosh-client with a stray token", []string{"mosh-client", "-# u@h top |", "1.2.3.4", "6"}, "", nil},
		{"mosh-client with predict", []string{"mosh-client", "-# --predict=always u@h |", "1.2.3.4", "6"}, "", nil},
		{"mosh-client with ssh", []string{"mosh-client", "-# --ssh=ssh u@h |", "1.2.3.4", "6"}, "", nil},
		{"mosh-client with client", []string{"mosh-client", "-# --client=/x u@h |", "1.2.3.4", "6"}, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, ok := parseRemoteLogin(c.argv)
			if !ok {
				if c.want != nil {
					t.Fatalf("not followed, want %q", c.want)
				}
				return
			}
			if c.want == nil {
				t.Fatalf("followed as %q, want not followed", l.argv(c.dir))
			}
			if got := l.argv(c.dir); !slices.Equal(got, c.want) {
				t.Fatalf("argv\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// A folder is used only when the report came from the machine the client
// connected to. A report from a further hop names a folder there, even when
// the two names share a first label.
func TestRemoteLoginHostMatches(t *testing.T) {
	cases := []struct {
		dest, reported string
		want           bool
	}{
		{"reachy-mini", "reachy-mini", true},
		{"pollen@reachy-mini", "Reachy-Mini", true},
		{"ssh://u@box.example.com:2222", "box.example.com", true},
		{"u@[::1]:22", "::1", true},
		{"pollen@reachy-mini.tail1234.ts.net", "reachy-mini", false},
		{"u@box.a.example", "box.b.example", false},
		{"u@box", "other", false},
		{"u@box", "", false},
	}
	for _, c := range cases {
		l := remoteLogin{dest: c.dest}
		if got := l.hostMatches(c.reported); got != c.want {
			t.Errorf("hostMatches(%q, %q) = %v, want %v", c.dest, c.reported, got, c.want)
		}
	}
}

// A destination that is an alias matches the host name ssh -G resolves it to,
// in full, or by its first label when the report has no dot.
func TestRemoteLoginReportMatchesResolvedHost(t *testing.T) {
	old := resolveSSHHostName
	t.Cleanup(func() { resolveSSHHostName = old })
	cases := []struct {
		dest, resolved, reported string
		want                     bool
	}{
		{"prod", "box.example.com", "box.example.com", true},
		{"prod", "box.example.com", "box", true},
		{"prod", "box.example.com", "box.other.com", false},
		{"prod", "box.example.com", "other", false},
		{"prod", "10.0.0.5", "10", false},
		{"prod", "", "box", false},
		{"box", "", "box", true},
	}
	for _, c := range cases {
		resolveSSHHostName = func(remoteLogin) string { return c.resolved }
		l := remoteLogin{dest: c.dest}
		if got := l.reportMatches(c.reported); got != c.want {
			t.Errorf("reportMatches(%q via %q, %q) = %v, want %v", c.dest, c.resolved, c.reported, got, c.want)
		}
	}
}

// The log line of a followed split carries no -o value.
func TestRedactSSHArgv(t *testing.T) {
	got := redactSSHArgv([]string{"/usr/bin/ssh", "-o", "User=me", "-o", "Ciphers aes", "-p", "22", "h"})
	want := []string{"/usr/bin/ssh", "-o", "User=...", "-o", "...", "-p", "22", "h"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
