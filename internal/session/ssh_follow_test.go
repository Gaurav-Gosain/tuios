package session

import (
	"slices"
	"testing"
)

// The argv an ssh split replays is a security boundary: it is a command line
// read from another process and exec'd again. The ways it can go wrong, and
// the cases below that pin each one:
//
//   - The remote command is replayed, so the split runs it a second time.
//   - An option that changes what the session is (-N, -f, -W, -O, -s, -T, or a
//     RemoteCommand -o) is replayed, so the split is not a shell.
//   - A forward is replayed, so the split fails to bind or doubles a tunnel.
//   - An option value is mistaken for the destination, or the destination for
//     an option value, so the split connects somewhere else.
//   - An unknown option is guessed at, and the rest of the line is misread.
//   - The remote folder breaks out of its quoting in the remote shell.
func TestParseRemoteLogin(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		exe  string
		dir  string
		want []string // nil: not followed
	}{
		{"plain", []string{"ssh", "host"}, "", "", []string{"ssh", "host"}},
		{"exe wins", []string{"ssh", "host"}, "/usr/bin/ssh", "", []string{"/usr/bin/ssh", "host"}},
		{"remote command dropped", []string{"ssh", "-p", "2222", "u@h", "tail", "-f", "log"}, "", "",
			[]string{"ssh", "-p", "2222", "u@h"}},
		{"options kept", []string{"ssh", "-i", "/k", "-J", "jump", "-o", "User=x", "-l", "me", "h"}, "", "",
			[]string{"ssh", "-i", "/k", "-J", "jump", "-o", "User=x", "-l", "me", "h"}},
		{"attached values", []string{"ssh", "-p2222", "-oPort=1", "-i/k", "h"}, "", "",
			[]string{"ssh", "-p", "2222", "-o", "Port=1", "-i", "/k", "h"}},
		{"cluster with value", []string{"ssh", "-4Cp", "22", "h"}, "", "",
			[]string{"ssh", "-4", "-C", "-p", "22", "h"}},
		{"session changers dropped", []string{"ssh", "-N", "-f", "-T", "-n", "-tt", "-M", "-S", "/s", "h"}, "", "",
			[]string{"ssh", "-S", "/s", "h"}},
		{"forwards dropped", []string{"ssh", "-L", "8080:x:80", "-R8081:y:81", "-D", "1080", "-W", "x:1", "h"}, "", "",
			[]string{"ssh", "h"}},
		{"o options dropped", []string{"ssh", "-o", "RemoteCommand=top", "-oSessionType=none", "-o", "RequestTTY no", "h"}, "", "",
			[]string{"ssh", "h"}},
		{"options after destination", []string{"ssh", "h", "-p", "2222", "ls"}, "", "",
			[]string{"ssh", "-p", "2222", "h"}},
		{"double dash before destination", []string{"ssh", "-p", "1", "--", "h", "-p", "2"}, "", "",
			[]string{"ssh", "-p", "1", "h"}},
		{"double dash after destination", []string{"ssh", "h", "--", "ls"}, "", "", []string{"ssh", "h"}},
		{"unknown option", []string{"ssh", "-Z", "h"}, "", "", nil},
		{"missing value", []string{"ssh", "-p"}, "", "", nil},
		{"no destination", []string{"ssh", "-v"}, "", "", nil},
		{"not ssh", []string{"vim", "h"}, "", "", nil},
		{"sshd is not ssh", []string{"sshd", "-D"}, "", "", nil},
		{"script", []string{"/bin/sh", "/opt/bin/ssh", "h", "ls"}, "/usr/bin/dash", "",
			[]string{"/opt/bin/ssh", "h"}},
		{"shell running code", []string{"sh", "-c", "x /opt/ssh", "h"}, "", "", nil},
		{"perl running code", []string{"perl", "-e", "1", "/usr/bin/ssh", "h"}, "", "", nil},
		{"script with interpreter option", []string{"/usr/bin/perl", "-w", "/usr/bin/mosh", "h"}, "", "",
			[]string{"/usr/bin/mosh", "h"}},
		{"remote folder", []string{"ssh", "-p", "22", "h", "ls"}, "", "/srv/a b",
			[]string{"ssh", "-p", "22", "-t", "h", `cd '/srv/a b' 2>/dev/null; exec "$SHELL" -l`}},
		{"folder with quote", []string{"ssh", "h"}, "", "/x'; rm -rf ~; '",
			[]string{"ssh", "-t", "h", `cd '/x'"'"'; rm -rf ~; '"'"'' 2>/dev/null; exec "$SHELL" -l`}},
		{"folder with backslash", []string{"ssh", "h"}, "", `/x\'`, []string{"ssh", "h"}},
		{"folder with newline", []string{"ssh", "h"}, "", "/x\nrm", []string{"ssh", "h"}},
		{"relative folder", []string{"ssh", "h"}, "", "x", []string{"ssh", "h"}},
		{"mosh", []string{"mosh", "--ssh=ssh -p 2", "-p", "6000", "h", "--", "top"}, "", "",
			[]string{"mosh", "--ssh=ssh -p 2", "--port=6000", "h"}},
		{"mosh folder", []string{"mosh", "h"}, "", "/srv",
			[]string{"mosh", "h", "--", "sh", "-c", `cd '/srv' 2>/dev/null; exec "$SHELL" -l`}},
		{"mosh unknown option", []string{"mosh", "--frob", "h"}, "", "", nil},
		{"mosh-client", []string{"mosh-client", "-# --predict=always u@h |", "100.1.2.3", "60001"}, "", "",
			[]string{"mosh", "--predict=always", "u@h"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, ok := parseRemoteLogin(c.argv, c.exe)
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
// connected to. A report from a further hop names a folder there.
func TestRemoteLoginHostMatches(t *testing.T) {
	cases := []struct {
		dest, reported string
		want           bool
	}{
		{"reachy-mini", "reachy-mini", true},
		{"pollen@reachy-mini", "Reachy-Mini", true},
		{"pollen@reachy-mini.tail1234.ts.net", "reachy-mini", true},
		{"ssh://u@box.example.com:2222", "box", true},
		{"u@[::1]:22", "::1", true},
		{"u@box", "other", false},
		{"10.0.0.1", "10", false},
		{"u@box", "", false},
	}
	for _, c := range cases {
		l := remoteLogin{dest: c.dest}
		if got := l.hostMatches(c.reported); got != c.want {
			t.Errorf("hostMatches(%q, %q) = %v, want %v", c.dest, c.reported, got, c.want)
		}
	}
}
