package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Managing a host the way a person does: from the command line, against a
// daemon that is already running, with no restart anywhere.
//
// This is the reachability proof for the whole feature. Every part of it is the
// real thing: the real CLI binary, the real config file, the real daemon, the
// real subprocess transport and the real stdio proxy. The only stand-in is ssh
// itself, and it is the one from sidebar_hosts_test.go: it drops ssh's options
// and the address and runs the command locally, so nothing reads the
// developer's ssh config, known_hosts or agent, and no network connection is
// made.
//
// What would pass a weaker test and fail this one: a command that writes the
// config file but never reaches the daemon, or a daemon that reads the table
// once at start. Both were the state of the world before this branch.

// tuiosCLIEnv is tuiosCLI with extra environment, which the host commands need:
// TUIOS_SSH names the ssh stand-in, and `tuios hosts test` dials in the CLI's
// own process rather than the daemon's.
func tuiosCLIEnv(t *testing.T, base string, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitForHostListing polls `tuios hosts` until the output satisfies want.
func waitForHostListing(t *testing.T, base string, want func(string) bool, why string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "hosts")
		if want(out) {
			return out
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s\nThe last listing was:\n%s", why, out)
	return out
}

// TestAddingAHostReachesTheRunningDaemon is add, test and remove, in the order
// a person does them, against one daemon that is never restarted.
func TestAddingAHostReachesTheRunningDaemon(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}

	// The daemon starts with no hosts at all, which is the default install.
	term := startIn(t, base, startOpts{args: []string{"new", "fed-add"}, env: env})
	waitBoot(t, term)

	out, err := tuiosCLI(t, base, "hosts")
	if err != nil {
		t.Fatalf("tuios hosts: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No hosts are configured") {
		t.Fatalf("the daemon did not start with an empty host table:\n%s", out)
	}

	// The command a person types. --command points the far side at this test's
	// own binary, which is what the ssh stand-in ends up running.
	out, err = tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox",
		"--command", tuiosBin, "--connect-timeout", "5")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts add' failed: %v\n%s", err, out)
	}
	t.Logf("tuios hosts add:\n%s", out)

	// No restart, no kill-server, no second daemon. The listing has to change on
	// its own because the daemon follows the file.
	listing := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the host added from the command line never came up in the running daemon")
	t.Logf("tuios hosts after add:\n%s", listing)

	// `tuios hosts test` dials the machine itself and says what happened.
	out, err = tuiosCLIEnv(t, base, env, "hosts", "test", "build")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts test' failed against a host that is up: %v\n%s", err, out)
	}
	if !strings.Contains(out, "The host answers") {
		t.Errorf("ASSERTION: 'tuios hosts test' did not report the host as answering:\n%s", out)
	}
	t.Logf("tuios hosts test:\n%s", out)

	// Removing it closes the link, again with no restart.
	out, err = tuiosCLIEnv(t, base, env, "hosts", "remove", "build")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts remove' failed: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "No hosts are configured")
	}, "the host removed from the command line stayed in the running daemon")
}

// TestHostTestReportsWhatSSHSaid is the reason `tuios hosts test` exists: when
// the link fails, the words that explain it come from ssh, and they have to
// reach the person who ran the command.
func TestHostTestReportsWhatSSHSaid(t *testing.T) {
	base := t.TempDir()
	env := []string{"TUIOS_SSH=" + writeRefusingSSH(t, filepath.Join(base, "bin"))}

	// The add dials once and says what ssh said, and the host is added all
	// the same: a refusal is something to fix, not a reason to forget the
	// machine.
	out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox", "--connect-timeout", "2")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts add' failed against a host ssh refused; the host must be added anyway: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Permission denied") {
		t.Errorf("ASSERTION: what ssh said never reached the user at add time:\n%s", out)
	}
	t.Logf("tuios hosts add against a refused host:\n%s", out)

	out, err = tuiosCLIEnv(t, base, env, "hosts", "test", "build")
	if err == nil {
		t.Errorf("ASSERTION: 'tuios hosts test' succeeded against a host ssh refused:\n%s", out)
	}
	if !strings.Contains(out, "Permission denied") {
		t.Errorf("ASSERTION: what ssh said never reached the user:\n%s", out)
	}
	t.Logf("tuios hosts test against a refused host:\n%s", out)

	// The refused key is one of six ways ssh fails, and each needs another
	// fix. Every class gets the words a real OpenSSH 10 printed for it, and
	// the report must name the class and its fix, not "did not answer".
	classes := writeFailingSSH(t, filepath.Join(base, "bin"))
	env = []string{"TUIOS_SSH=" + classes}
	for _, c := range sshFailureCases {
		name := strings.ReplaceAll(c.kind, "_", "-")
		if out, err := tuiosCLIEnv(t, base, env, "hosts", "add", name, "someone@"+name, "--connect-timeout", "2"); err != nil {
			t.Fatalf("tuios hosts add %s: %v\n%s", name, err, out)
		}
		out, _ := tuiosCLIEnv(t, base, env, "hosts", "test", name, "--json")
		var r struct {
			Status     string `json:"status"`
			Reason     string `json:"reason"`
			Failure    string `json:"failure"`
			Fix        string `json:"fix"`
			FixCommand string `json:"fix_command"`
		}
		// The output is the JSON, then the error line the command fails with.
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("tuios hosts test %s --json printed no JSON: %v\n%s", name, err, out)
		}
		if r.Failure != c.kind {
			t.Errorf("ASSERTION: ssh said %q and the report named the failure %q, want %q", c.stderr, r.Failure, c.kind)
		}
		if !strings.Contains(r.Reason, c.reason) || !strings.Contains(r.Fix, c.fix) {
			t.Errorf("ASSERTION: %s: reason %q and fix %q, want them to say %q and %q", c.kind, r.Reason, r.Fix, c.reason, c.fix)
		}
		if r.FixCommand != c.command {
			t.Errorf("ASSERTION: %s: the fix command is %q, want %q", c.kind, r.FixCommand, c.command)
		}
		plain, _ := tuiosCLIEnv(t, base, env, "hosts", "test", name)
		if !strings.Contains(plain, c.fix) {
			t.Errorf("ASSERTION: tuios hosts test %s does not print the fix:\n%s", name, plain)
		}
		t.Logf("tuios hosts test %s:\n%s", name, plain)
	}
}

// sshFailureCases are the six classes of ssh failure, each with what OpenSSH
// 10.6 wrote on stderr for it (captured against real servers, addresses
// changed), and what the report must say.
var sshFailureCases = []struct {
	kind, stderr, reason, fix, command string
}{
	{"host_key_new", "No ED25519 host key is known for 10.0.0.12 and you have requested strict checking.\nHost key verification failed.",
		"does not know the host key", "check the key", "ssh someone@host-key-new true"},
	{"host_key_changed", "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\n@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\nHost key for 10.0.0.12 has changed and you have requested strict checking.\nHost key verification failed.",
		"This can be an attack", "does not accept a changed key", ""},
	{"auth", "someone@10.0.0.12: Permission denied (publickey,password).",
		"refused your ssh key", "Copy your ssh key", "ssh-copy-id someone@auth"},
	{"dns", "ssh: Could not resolve hostname nobox: Name or service not known",
		"cannot find the address", "Check the address", ""},
	{"refused", "ssh: connect to host 10.0.0.12 port 22: Connection refused",
		"refused the connection", "Start the ssh server", ""},
	{"timeout", "ssh: connect to host 10.0.0.12 port 22: Connection timed out",
		"does not answer", "Turn on the host", ""},
}

// writeFailingSSH puts an ssh stand-in in dir that fails, for each address in
// sshFailureCases, with the words ssh printed for that class and exit 255.
func writeFailingSSH(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\ncase \"$1\" in\n")
	for _, c := range sshFailureCases {
		fmt.Fprintf(&b, "  someone@%s) cat >&2 <<'EOF'\n%s\nEOF\n  ;;\n", strings.ReplaceAll(c.kind, "_", "-"), c.stderr)
	}
	b.WriteString("esac\nexit 255\n")
	ssh := filepath.Join(dir, "failing-ssh-classes")
	if err := os.WriteFile(ssh, []byte(b.String()), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return ssh
}

// writeRefusingSSH puts an ssh stand-in in dir that fails the way ssh does
// against a machine that refuses the key: a message on stderr and exit 255.
func writeRefusingSSH(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ssh := filepath.Join(dir, "failing-ssh")
	script := "#!/bin/sh\necho 'someone@buildbox: Permission denied (publickey).' >&2\nexit 255\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return ssh
}

// TestAddingAHostKeepsTheRestOfTheConfigFile is the file half. A command that
// adds one machine must not rewrite what the user hand-wrote around it.
func TestAddingAHostKeepsTheRestOfTheConfigFile(t *testing.T) {
	base := t.TempDir()
	// The add dials the host once. The stand-in refuses, so nothing reaches
	// the network and nothing reads the developer's ssh configuration.
	env := []string{"TUIOS_SSH=" + writeRefusingSSH(t, filepath.Join(base, "bin"))}
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	path := filepath.Join(dir, "config.toml")
	body := "# A note I wrote myself.\n[appearance]\n# the border I like\nborder_style = \"double\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox")
	if err != nil {
		t.Fatalf("tuios hosts add: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the config this test wrote
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	for _, want := range []string{"# A note I wrote myself.", "# the border I like", `border_style = "double"`} {
		if !strings.Contains(got, want) {
			t.Errorf("ASSERTION: adding a host lost %q from the config file:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[hosts.build]") {
		t.Errorf("ASSERTION: the host is not in the config file:\n%s", got)
	}
}
