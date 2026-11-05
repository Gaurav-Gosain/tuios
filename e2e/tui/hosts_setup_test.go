package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tuios hosts setup, the whole way, against two real daemons.
//
// buildbox is a fresh machine: its own HOME, no tuios anywhere it looks, and
// a PATH that starts with its own ~/.local/bin. laptopbox is this machine, as
// buildbox reaches it back. One ssh stand-in routes both addresses, and it is
// the ssh both machines run, so the far side's test of its way back and its
// link to this machine go through it too. Everything else is real: the probe
// and install scripts in a real sh, the copy of this tuios over the pipe,
// the far daemon started by the setup, the config files on both sides, and
// the links.
//
// The proof is the end state a person wants: a link each way, and an agent
// on buildbox that reaches this machine's Inbox.

// writeSetupSSH writes the ssh stand-in for the setup test. someone@buildbox
// runs in far, with far's HOME and a PATH with no tuios on it.
// someone@laptopbox runs in here, with this test's tuios on the PATH, the
// way a real machine has its own tuios installed.
func writeSetupSSH(t *testing.T, here, far string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) shift 2 ;; -T|-t) shift ;; --) shift; break ;; *) break ;; esac; done\n")
	b.WriteString("addr=\"$1\"\nshift\ncase \"$addr\" in\n")
	for _, m := range []struct{ addr, base, path string }{
		{"someone@buildbox", far, filepath.Join(xdgDir(far, "HOME"), ".local", "bin") + ":/usr/bin:/bin"},
		{"someone@laptopbox", here, filepath.Dir(tuiosBin) + ":/usr/bin:/bin"},
	} {
		fmt.Fprintf(&b, "  %s)\n", m.addr)
		for _, key := range xdgKeys {
			fmt.Fprintf(&b, "    export %s=%s\n", key, xdgDir(m.base, key))
		}
		fmt.Fprintf(&b, "    export PATH=%s SHELL=/bin/sh\n    ;;\n", m.path)
	}
	b.WriteString("  *) echo \"ssh: Could not resolve hostname $addr: Name or service not known\" >&2; exit 255 ;;\nesac\n")
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	ssh := filepath.Join(here, "fake-ssh-setup")
	if err := os.WriteFile(ssh, []byte(b.String()), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return ssh
}

// setupResult is the part of the setup JSON the test reads.
type setupResult struct {
	OK        bool   `json:"ok"`
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Error     string `json:"error"`
	Probe     struct {
		OS    string `json:"os"`
		Tuios string `json:"tuios"`
	} `json:"probe"`
	Steps []struct {
		Step  string `json:"step"`
		State string `json:"state"`
		Text  string `json:"text"`
	} `json:"steps"`
	Host *struct {
		Status string `json:"status"`
	} `json:"host"`
	BothWays *struct {
		Name   string `json:"name"`
		Addr   string `json:"addr"`
		Added  bool   `json:"added"`
		Status string `json:"status"`
		WhyNot string `json:"why_not"`
	} `json:"both_ways"`
}

func decodeSetup(t *testing.T, out string) setupResult {
	t.Helper()
	var r setupResult
	i := strings.IndexByte(out, '{')
	if i < 0 {
		t.Fatalf("tuios hosts setup printed no JSON:\n%s", out)
	}
	if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&r); err != nil {
		t.Fatalf("tuios hosts setup printed bad JSON: %v\n%s", err, out)
	}
	return r
}

func exitStatus(err error) int {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// TestSetUpAMachineBothWays sets up a machine with no tuios from one command,
// with both directions, and an agent there reaches the Inbox here.
func TestSetUpAMachineBothWays(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	env := []string{"TUIOS_SSH=" + writeSetupSSH(t, here, far)}

	// The daemon here runs, as it does for a person who is in tuios.
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}

	// 1. With no terminal and no --install, the setup stops before it
	// changes anything, names the flag, and exits 2.
	out, err := tuiosCLIEnv(t, here, env, "hosts", "setup", "someone@buildbox", "--name", "build", "--yes")
	if exitStatus(err) != 2 || !strings.Contains(out, "--install copy, release, pkg or script") {
		t.Fatalf("ASSERTION: a setup with no --install must exit 2 and name the flag, got %v:\n%s", err, out)
	}
	if _, statErr := os.Stat(farBin); statErr == nil {
		t.Fatalf("ASSERTION: the setup that stopped installed tuios on buildbox")
	}
	t.Logf("no --install:\n%s", out)

	// 2. A dry run probes, prints every command, and changes nothing.
	out, err = tuiosCLIEnv(t, here, env, "hosts", "setup", "someone@buildbox", "--name", "build", "--install", "copy", "--both-ways", "--address", "someone@laptopbox", "--dry-run")
	if err != nil {
		t.Fatalf("ASSERTION: the dry run failed: %v\n%s", err, out)
	}
	for _, want := range []string{"tuios is not installed on build.", "This changed nothing.", "The plan. Nothing runs.", "start-server", "write [hosts.build]", "ssh -o BatchMode=yes someone@laptopbox true", "hosts add"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: the dry run does not say %q:\n%s", want, out)
		}
	}
	if _, statErr := os.Stat(farBin); statErr == nil {
		t.Fatalf("ASSERTION: the dry run installed tuios on buildbox")
	}
	if hosts, _ := os.ReadFile(filepath.Join(xdgDir(here, "XDG_CONFIG_HOME"), "tuios", "config.toml")); strings.Contains(string(hosts), "[hosts.build]") {
		t.Fatalf("ASSERTION: the dry run wrote [hosts.build]:\n%s", hosts)
	}
	t.Logf("dry run:\n%s", out)

	// 3. The setup itself.
	out, err = tuiosCLIEnv(t, here, env, "hosts", "setup", "someone@buildbox", "--name", "build", "--install", "copy", "--both-ways", "--address", "someone@laptopbox", "--yes", "--json")
	t.Logf("tuios hosts setup --json:\n%s", out)
	if err != nil {
		t.Fatalf("ASSERTION: the setup failed: %v", err)
	}
	r := decodeSetup(t, out)
	if !r.OK || r.Installed != "copy" {
		t.Fatalf("ASSERTION: the setup did not report a copy that worked: ok=%v installed=%q error=%q", r.OK, r.Installed, r.Error)
	}
	if r.Host == nil || r.Host.Status != "up" {
		t.Fatalf("ASSERTION: the link to build is not up at the end of the setup: %+v", r.Host)
	}
	if r.BothWays == nil || !r.BothWays.Added || r.BothWays.Status != "up" || r.BothWays.Addr != "someone@laptopbox" {
		t.Fatalf("ASSERTION: this machine was not added on buildbox with a link that is up: %+v", r.BothWays)
	}
	if _, statErr := os.Stat(farBin); statErr != nil {
		t.Fatalf("ASSERTION: tuios is not at ~/.local/bin/tuios on buildbox: %v", statErr)
	}
	farCfg, _ := os.ReadFile(filepath.Join(xdgDir(far, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
	if !strings.Contains(string(farCfg), "[hosts."+r.BothWays.Name+"]") {
		t.Fatalf("ASSERTION: buildbox has no [hosts.%s]:\n%s", r.BothWays.Name, farCfg)
	}

	// 4. The daemon here holds the link without a restart.
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon here never reported build up after the setup")

	// 5. An agent on buildbox reaches the Inbox here.
	if out, err := tuiosCLI(t, far, "new", "far", "--detach"); err != nil {
		t.Fatalf("create a session on buildbox: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, far, "set-agent-state", "-s", "far", "errored", "-m", "rate limited by the api"); err != nil {
		t.Fatalf("set-agent-state on buildbox: %v\n%s", err, out)
	}
	att := waitForAttention(t, here, env, "build:far", "rate limited by the api")
	t.Logf("list-attention here:\n%s", att)
}

// writeFakeTailscale puts a tailscale stand-in in dir whose status names
// this machine, buildbox (online, linux) and sleepy (offline).
func writeFakeTailscale(t *testing.T, dir string) {
	t.Helper()
	status := `{"Self":{"HostName":"laptop","DNSName":"laptop.example.ts.net.","OS":"linux","UserID":1,"TailscaleIPs":["100.64.0.1"],"Online":true},` +
		`"Peer":{"k1":{"HostName":"buildbox","DNSName":"buildbox.example.ts.net.","OS":"linux","UserID":1,"TailscaleIPs":["100.64.0.2"],"Online":true},` +
		`"k2":{"HostName":"sleepy","DNSName":"sleepy.example.ts.net.","OS":"linux","UserID":1,"TailscaleIPs":["100.64.0.3"],"Online":false}},` +
		`"User":{"1":{"LoginName":"someone@example.com"}}}`
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\ncat <<'EOF'\n" + status + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(script), 0o700); err != nil { //nolint:gosec // a tailscale stand-in this test runs
		t.Fatalf("write the tailscale stand-in: %v", err)
	}
}

// TestSetUpTailnetMachines lists the tailnet with each machine's state, and
// sets up only the machine that was named. The offline one is listed and
// never dialed.
func TestSetUpTailnetMachines(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	ssh := writeSetupSSH(t, here, far)
	// The tailnet address of buildbox reaches the same far machine.
	data, err := os.ReadFile(ssh)
	if err != nil {
		t.Fatal(err)
	}
	routed := strings.Replace(string(data), "  someone@buildbox)", "  someone@buildbox|buildbox.example.ts.net)", 1)
	// A dial of sleepy fails the test: an offline machine is never dialed.
	routed = strings.Replace(routed, "case \"$addr\" in", "case \"$addr\" in\n  sleepy*) echo dialed >"+filepath.Join(here, "sleepy-dialed")+"; exit 255 ;;", 1)
	if err := os.WriteFile(ssh, []byte(routed), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatal(err)
	}
	tsDir := filepath.Join(here, "tsbin")
	writeFakeTailscale(t, tsDir)
	env := []string{"TUIOS_SSH=" + ssh, "PATH=" + tsDir + ":" + os.Getenv("PATH")}
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}

	out, err := tuiosCLIEnv(t, here, env, "hosts", "setup", "--tailnet", "--dry-run")
	t.Logf("tuios hosts setup --tailnet --dry-run:\n%s", out)
	if err != nil {
		t.Fatalf("ASSERTION: the tailnet list failed: %v", err)
	}
	if !containsAll(out, "buildbox", "no tuios", "sleepy", "offline", "Pass --all, or --only") {
		t.Errorf("ASSERTION: the list does not show buildbox without tuios and sleepy offline")
	}

	out, err = tuiosCLIEnv(t, here, env, "hosts", "setup", "--tailnet")
	if exitStatus(err) != 2 {
		t.Errorf("ASSERTION: a tailnet setup with no --all or --only must exit 2, got %v:\n%s", err, out)
	}

	out, err = tuiosCLIEnv(t, here, env, "hosts", "setup", "--tailnet", "--only", "buildbox", "--install", "copy", "--yes", "--json")
	t.Logf("tuios hosts setup --tailnet --only buildbox --json:\n%s", out)
	if err != nil {
		t.Fatalf("ASSERTION: the tailnet setup failed: %v", err)
	}
	var res struct {
		OK      bool `json:"ok"`
		Summary struct {
			Ready, Failed int
		} `json:"summary"`
		Machines []setupResult `json:"machines"`
	}
	if err := json.NewDecoder(strings.NewReader(out[strings.IndexByte(out, '{'):])).Decode(&res); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !res.OK || res.Summary.Ready != 1 || len(res.Machines) != 1 || res.Machines[0].Host == nil || res.Machines[0].Host.Status != "up" {
		t.Fatalf("ASSERTION: buildbox is not ready with a link that is up: %+v", res)
	}
	if _, statErr := os.Stat(farBin); statErr != nil {
		t.Fatalf("ASSERTION: tuios is not installed on buildbox: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(here, "sleepy-dialed")); statErr == nil {
		t.Fatalf("ASSERTION: the offline machine was dialed")
	}
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "buildbox", "up")
	}, "the daemon here never reported buildbox up after the tailnet setup")
}
