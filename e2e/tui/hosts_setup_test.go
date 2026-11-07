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
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
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
	// This machine's own tuios, on its PATH by that name, whatever the test
	// binary is called: the far side's link looks for "tuios".
	hereBin := filepath.Join(here, "bin")
	if err := os.MkdirAll(hereBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tuiosBin, filepath.Join(hereBin, "tuios")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) shift 2 ;; -T|-t) shift ;; --) shift; break ;; *) break ;; esac; done\n")
	b.WriteString("addr=\"$1\"\nshift\ncase \"$addr\" in\n")
	for _, m := range []struct{ addr, base, path string }{
		{"someone@buildbox", far, filepath.Join(xdgDir(far, "HOME"), ".local", "bin") + ":/usr/bin:/bin"},
		{"someone@laptopbox", here, hereBin + ":/usr/bin:/bin"},
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
	out, err = tuiosCLIEnv(t, here, env, "hosts", "setup", "someone@buildbox", "--name", "build", "--install", "copy", "--both-ways", "--address", "someone@laptopbox", "--as", "laptop", "--yes", "--json")
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
	if r.BothWays == nil || !r.BothWays.Added || r.BothWays.Status != "up" || r.BothWays.Addr != "someone@laptopbox" || r.BothWays.Name != "laptop" {
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

// writeTailnetSetupSSH is writeSetupSSH where the tailnet name of buildbox
// reaches the same far machine, and a dial of sleepy, the offline machine,
// leaves a mark in here.
func writeTailnetSetupSSH(t *testing.T, here, far string) string {
	t.Helper()
	ssh := writeSetupSSH(t, here, far)
	data, err := os.ReadFile(ssh)
	if err != nil {
		t.Fatal(err)
	}
	routed := strings.Replace(string(data), "  someone@buildbox)", "  someone@buildbox|buildbox.example.ts.net)", 1)
	routed = strings.Replace(routed, "case \"$addr\" in", "case \"$addr\" in\n  sleepy*) echo dialed >"+filepath.Join(here, "sleepy-dialed")+"; exit 255 ;;", 1)
	if err := os.WriteFile(ssh, []byte(routed), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatal(err)
	}
	return ssh
}

// TestSetUpTailnetMachines lists the tailnet with each machine's state, and
// sets up only the machine that was named. The offline one is listed and
// never dialed.
func TestSetUpTailnetMachines(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	ssh := writeTailnetSetupSSH(t, here, far)
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

// TestSetUpOffersNoReleaseForADevBuild is a tuios built from a checkout at a
// Go pseudo-version. The version parses as one with a prerelease, and no
// release of that name exists, so the setup must offer a copy and never the
// release.
func TestSetUpOffersNoReleaseForADevBuild(t *testing.T) {
	const pseudo = "v0.12.2-0.20261105101010-abcdef123456"
	dir := t.TempDir()
	bin := filepath.Join(dir, "tuios")
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+pseudo, "-o", bin, "./cmd/tuios")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the pseudo-version tuios: %v\n%s", err, out)
	}
	here := remoteMachine(t)
	far := remoteMachine(t)
	env := []string{"TUIOS_SSH=" + writeSetupSSH(t, here, far)}
	cmd := exec.Command(bin, "hosts", "setup", "someone@buildbox", "--name", "build", "--dry-run", "--json")
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(here, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, _ := cmd.CombinedOutput()
	t.Logf("a dry run of a pseudo-version build:\n%s", out)
	var r struct {
		Options []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
		} `json:"options"`
	}
	if err := json.NewDecoder(strings.NewReader(string(out[strings.IndexByte(string(out), '{'):]))).Decode(&r); err != nil {
		t.Fatalf("no JSON: %v", err)
	}
	copyOK := false
	for _, o := range r.Options {
		if o.ID == "release" {
			t.Errorf("ASSERTION: a pseudo-version build offers the release %s, which does not exist", pseudo)
		}
		if o.ID == "copy" && o.Available {
			copyOK = true
		}
	}
	if !copyOK {
		t.Errorf("ASSERTION: a pseudo-version build does not offer a copy of itself: %+v", r.Options)
	}
}

// TestSetUpAMachineOnATerminal runs tuios hosts setup on a terminal, where it
// asks for each choice the flags did not make, and answers with the keys a
// person presses. The install list takes enter (a copy is the first choice),
// start at boot takes n, and both ways takes y; --address comes as a flag, so
// it is not asked, and --as names this machine on buildbox. The end is the
// done summary, with the path there under ~ and [hosts.laptop] on buildbox,
// and a link that is up.
//
// Before that, ctrl+c at the install list stops the setup: no [hosts.build]
// here, and no tuios on buildbox.
//
// Frames of the install list and the done summary go under artifactDir as
// PNGs in the frame font.
func TestSetUpAMachineOnATerminal(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	hereCfg := filepath.Join(xdgDir(here, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	env := []string{"TUIOS_SSH=" + writeSetupSSH(t, here, far), "COLORTERM=truecolor"}
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}
	frames := artifactDir(t)
	frame := func(term *tuitest.Terminal, name string) {
		t.Helper()
		saveArtifact(t, term, frames, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, name)
		t.Logf("frame %s saved under %s", name, frames)
	}
	setup := func() *tuitest.Terminal {
		return startIn(t, here, startOpts{cols: 110, rows: 44, animations: true, env: env,
			args: []string{"hosts", "setup", "someone@buildbox", "--name", "build", "--address", "someone@laptopbox", "--as", "laptop"}})
	}
	press := func(term *tuitest.Terminal, keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %q: %v", keys, err)
		}
	}
	installList := func(s tuitest.Screen) bool {
		return screenHas(s, "tuios is not installed on build. Pick how to install it.", "> Copy this tuios", "enter pick")
	}
	const slow = 90 * time.Second

	// 1. ctrl+c at the install list.
	term := setup()
	waitFor(t, term, "the install list", installList, slow)
	press(term, tuitest.Ctrl('c'))
	waitFor(t, term, "the stop line", func(s tuitest.Screen) bool { return screenHas(s, "Stopped. Nothing was added.") }, slow)
	if cfg, _ := os.ReadFile(hereCfg); strings.Contains(string(cfg), "[hosts.build]") {
		t.Fatalf("ASSERTION: the stopped setup wrote [hosts.build]:\n%s", cfg)
	}
	if _, err := os.Stat(farBin); err == nil {
		t.Fatalf("ASSERTION: the stopped setup installed tuios on buildbox")
	}

	// 2. The whole setup, with the keys a person presses.
	term = setup()
	waitFor(t, term, "the install list", installList, slow)
	frame(term, "setup-install-list")
	press(term, tuitest.Enter)
	boot, both := "Start tuios on build when it boots?", "Add this machine on build too?"
	waitFor(t, term, "the next question", func(s tuitest.Screen) bool {
		return screenHas(s, both, "y yes") || screenHas(s, boot, "y yes")
	}, slow)
	if screenHas(term.Screen(), boot, "y yes") {
		press(term, "n")
		waitFor(t, term, "the boot answer", func(s tuitest.Screen) bool { return screenHas(s, "tuios does not start at boot on build.") }, slow)
	}
	waitFor(t, term, "the both-ways question", func(s tuitest.Screen) bool { return screenHas(s, both, "y yes") }, slow)
	frame(term, "setup-both-ways")
	press(term, "y")
	waitFor(t, term, "the done summary", func(s tuitest.Screen) bool {
		return screenHas(s, "build is ready. Its agents reach your Inbox.", "[hosts.build]", "tuios attach --host build")
	}, slow)
	frame(term, "setup-done")
	for _, want := range []string{"is installed at ~/.local/bin/tuios.", "The link to build is up.", "copied to ~/.local/bin/tuios", "Added on build", "[hosts.laptop]"} {
		if !screenHas(term.Screen(), want) {
			t.Errorf("ASSERTION: the done screen does not say %q:\n%s", want, term.Snapshot())
		}
	}

	if cfg, _ := os.ReadFile(hereCfg); !strings.Contains(string(cfg), "[hosts.build]") {
		t.Fatalf("ASSERTION: the setup did not write [hosts.build]:\n%s", cfg)
	}
	if _, err := os.Stat(farBin); err != nil {
		t.Fatalf("ASSERTION: tuios is not at ~/.local/bin/tuios on buildbox: %v", err)
	}
	farCfg, _ := os.ReadFile(filepath.Join(xdgDir(far, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
	if !strings.Contains(string(farCfg), "[hosts.laptop]") {
		t.Fatalf("ASSERTION: buildbox has no entry for this machine, after yes to both ways:\n%s", farCfg)
	}
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon here never reported build up after the setup")
}

// TestSetUpTailnetMachinesOnATerminal runs tuios hosts setup --tailnet on a
// terminal, with the keys a person presses. The tailnet has four machines:
//
//   - buildbox, with no tuios. o gives it its own answers: a copy.
//   - checkbox, with no tuios, behind a Tailscale check until the file
//     signed-in exists. o gives it a copy too. At its fix, enter opens the
//     sign-in page (this machine has no desktop, so the page only shows), the
//     test makes the file, and t on the person step tests again.
//   - authbox, which refuses the ssh key. space ticks it, and s at its fix
//     skips it.
//   - sleepy, offline: listed, never ticked, never dialed.
//
// The shared answer is --install none, so a machine without its own answers
// installs nothing. buildbox and checkbox end ready with tuios installed only
// when o gave them a copy. The flags keep the run off this machine's own
// systemd and its way back: --no-service, --one-way.
//
// Frames of the checklist and the summary go under artifactDir.
func TestSetUpTailnetMachinesOnATerminal(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	far2 := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	farBin2 := filepath.Join(xdgDir(far2, "HOME"), ".local", "bin", "tuios")
	signedIn := filepath.Join(here, "signed-in")
	// The stand-in for far2 first, to take its machine block for checkbox.
	data2, err := os.ReadFile(writeSetupSSH(t, here, far2))
	if err != nil {
		t.Fatal(err)
	}
	ssh := writeSetupSSH(t, here, far)
	data, err := os.ReadFile(ssh)
	if err != nil {
		t.Fatal(err)
	}
	head := "  someone@buildbox)\n"
	block := string(data2)[strings.Index(string(data2), head)+len(head):]
	block = block[:strings.Index(block, "    ;;\n")]
	extra := "  sleepy*) echo dialed >" + filepath.Join(here, "sleepy-dialed") + "; exit 255 ;;\n" +
		"  authbox*) echo \"authbox: Permission denied (publickey).\" >&2; exit 255 ;;\n" +
		"  checkbox*)\n    if [ ! -f " + signedIn + " ]; then echo \"# Tailscale SSH requires an additional check.\" >&2; " +
		"echo \"# To authenticate, visit: https://login.tailscale.com/a/abc123\" >&2; sleep 20; exit 255; fi\n" + block + "    ;;\n"
	routed := strings.Replace(string(data), "  someone@buildbox)", "  someone@buildbox|buildbox.example.ts.net)", 1)
	routed = strings.Replace(routed, "case \"$addr\" in\n", "case \"$addr\" in\n"+extra, 1)
	if err := os.WriteFile(ssh, []byte(routed), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatal(err)
	}
	tsDir := filepath.Join(here, "tsbin")
	writeFakeTailscale(t, tsDir)
	ts := filepath.Join(tsDir, "tailscale")
	tsData, err := os.ReadFile(ts)
	if err != nil {
		t.Fatal(err)
	}
	peer := func(key, name, ip string) string {
		return `,"` + key + `":{"HostName":"` + name + `","DNSName":"` + name + `.example.ts.net.","OS":"linux","UserID":1,"TailscaleIPs":["` + ip + `"],"Online":true}`
	}
	tsData = []byte(strings.Replace(string(tsData), `"Online":false}}`, `"Online":false}`+peer("k3", "authbox", "100.64.0.4")+peer("k4", "checkbox", "100.64.0.5")+"}", 1))
	if err := os.WriteFile(ts, tsData, 0o700); err != nil { //nolint:gosec // a tailscale stand-in this test runs
		t.Fatal(err)
	}
	env := []string{"TUIOS_SSH=" + ssh, "PATH=" + tsDir + ":" + os.Getenv("PATH"), "COLORTERM=truecolor"}
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}
	frames := artifactDir(t)
	frame := func(term *tuitest.Terminal, name string) {
		t.Helper()
		saveArtifact(t, term, frames, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, name)
		t.Logf("frame %s saved under %s", name, frames)
	}
	const slow = 90 * time.Second
	term := startIn(t, here, startOpts{cols: 110, rows: 44, animations: true, env: env,
		args: []string{"hosts", "setup", "--tailnet", "--install", "none", "--no-service", "--one-way"}})
	press := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %q: %v", keys, err)
		}
	}
	// cursorOn moves the checklist cursor down to the row of name.
	cursorOn := func(name string) {
		t.Helper()
		for range 6 {
			if screenHas(term.Screen(), "> [ ] "+name) || screenHas(term.Screen(), "> [x] "+name) {
				return
			}
			press(tuitest.Down)
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("the cursor never reached %s:\n%s", name, term.Snapshot())
	}
	ownCopy := func(name string) {
		t.Helper()
		cursorOn(name)
		press("o")
		waitFor(t, term, "the install list of "+name, func(s tuitest.Screen) bool {
			return screenHas(s, "How to install tuios on "+name+"?", "> The same version as here.")
		}, slow)
		press(tuitest.Enter)
		waitFor(t, term, "start at boot for "+name, func(s tuitest.Screen) bool { return screenHas(s, "Start tuios on "+name+" when it boots?") }, slow)
		press(tuitest.Enter)
		waitFor(t, term, "both ways for "+name, func(s tuitest.Screen) bool { return screenHas(s, "Add this machine on "+name+" too?") }, slow)
		press(tuitest.Enter)
	}

	waitFor(t, term, "the checklist", func(s tuitest.Screen) bool {
		return screenHas(s, "Pick the machines to set up.", "0 ticked", "authbox", "refused your ssh key", "checkbox", "Tailscale asks you to sign in", "sleepy", "offline", "o own answers")
	}, slow)
	cursorOn("authbox")
	press(" ")
	ownCopy("buildbox")
	ownCopy("checkbox")
	waitFor(t, term, "three ticked, two with own answers", func(s tuitest.Screen) bool {
		return screenHas(s, "3 ticked", "[x] authbox", "[x] buildbox", "[x] checkbox", "no install") && strings.Count(s.Text(), "own answers") >= 3
	}, slow)
	frame(term, "tailnet-checklist")
	press(tuitest.Enter)

	// The two fixes come in either order.
	skipped, signed := false, false
	for !skipped || !signed {
		waitFor(t, term, "a fix", func(s tuitest.Screen) bool {
			return (!skipped && screenHas(s, "authbox refused your ssh key.", "s skip authbox")) ||
				(!signed && screenHas(s, "Tailscale SSH waits for you to sign in.", "s skip checkbox"))
		}, slow)
		if !skipped && screenHas(term.Screen(), "authbox refused your ssh key.", "s skip authbox") {
			press("s")
			skipped = true
			continue
		}
		press(tuitest.Enter)
		waitFor(t, term, "the sign-in step", func(s tuitest.Screen) bool {
			return screenHas(s, "login.tailscale.com", "no desktop to open it", "t test again") && !screenHas(s, "s skip checkbox")
		}, slow)
		if err := os.WriteFile(signedIn, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		press("t")
		signed = true
	}
	waitFor(t, term, "the summary box", func(s tuitest.Screen) bool {
		return screenHas(s, "2 machines are ready. 1 needs you.", "authbox: ", "tuios hosts setup authbox")
	}, slow)
	frame(term, "tailnet-summary")

	for _, bin := range []string{farBin, farBin2} {
		if _, statErr := os.Stat(bin); statErr != nil {
			t.Fatalf("ASSERTION: o gave buildbox and checkbox a copy, and tuios is not at %s: %v", bin, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(here, "sleepy-dialed")); statErr == nil {
		t.Fatalf("ASSERTION: the offline machine was dialed")
	}
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "buildbox", "checkbox", "up")
	}, "the daemon here never reported buildbox and checkbox up after the tailnet setup")
}
