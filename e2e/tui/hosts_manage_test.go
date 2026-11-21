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

func tuiosCLIEnv(t *testing.T, base string, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	if out, done := simBeforeCLI(t, base, args); done {
		return out, nil
	}
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	for _, kv := range env {
		agentPathGuard(t, kv)
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func waitForHostListing(t *testing.T, base string, want func(string) bool, why string) string {
	t.Helper()
	return waitTextOf(t, why, 30*time.Second, func() string {
		out, _ := tuiosCLI(t, base, "hosts")
		return out
	}, want)
}

// TestAddingAHostReachesTheRunningDaemon is add, test and remove, in the order
// a person does them, against one daemon that is never restarted.
func TestAddingAHostReachesTheRunningDaemon(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "fed-add"}, env: env})
	waitBoot(t, term)

	out, err := tuiosCLI(t, base, "hosts")
	if err != nil {
		t.Fatalf("tuios hosts: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No hosts are configured") {
		t.Fatalf("the daemon did not start with an empty host table:\n%s", out)
	}

	out, err = tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox",
		"--command", tuiosBin, "--connect-timeout", "5")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts add' failed: %v\n%s", err, out)
	}
	t.Logf("tuios hosts add:\n%s", out)

	listing := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the host added from the command line never came up in the running daemon")
	t.Logf("tuios hosts after add:\n%s", listing)

	out, err = tuiosCLIEnv(t, base, env, "hosts", "test", "build")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios hosts test' failed against a host that is up: %v\n%s", err, out)
	}
	if !strings.Contains(out, "The host answers") {
		t.Errorf("ASSERTION: 'tuios hosts test' did not report the host as answering:\n%s", out)
	}
	t.Logf("tuios hosts test:\n%s", out)

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
