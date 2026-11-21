package tuie2e

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type pairedLink struct {
	base, remote string
	ak, own      string
	sshWrap      string
	env          []string
}

func setupPairedLink(t *testing.T, device string) *pairedLink {
	t.Helper()
	needTools(t, "ssh", "ssh-keygen")
	base := t.TempDir()
	remote := remoteMachine(t)
	dir := filepath.Join(base, "sshd")
	mustMkdir(dir)
	hostKey, _ := keygen(t, dir, "host", "-t", "ed25519")
	phoneKey, phoneLine := keygen(t, dir, "phone", "-t", "ed25519")
	_, ownLine := keygen(t, dir, "own", "-t", "ed25519")
	sshDir := filepath.Join(xdgDir(remote, "HOME"), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ak := filepath.Join(sshDir, "authorized_keys")
	own := ownLine + "\n"
	if err := os.WriteFile(ak, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(freePort(t))
	me, _ := user.Current()

	run := startPair(t, remote, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostKey+".pub", "--advertise-ssh", "127.0.0.1:"+port, "--timeout", "1m")
	ph := run.phone(t)
	_, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, device, phoneLine))
	if err != nil || !reply.OK {
		t.Fatalf("pairing failed: %+v %v\nstderr:\n%s", reply, err, run.stderr)
	}
	if _, err := run.wait(t, 15*time.Second); err != nil {
		t.Fatalf("tuios pair exit: %v\nstderr:\n%s", err, run.stderr)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("[127.0.0.1]:"+port+" "+reply.HostKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	startScratchSSHD(t, dir, hostKey, ak, port, remote)
	sshWrap := filepath.Join(dir, "ssh")
	wrap := "#!/bin/sh\nexec ssh -F /dev/null -p " + port +
		" -o UserKnownHostsFile=" + knownHosts + " -o GlobalKnownHostsFile=/dev/null -o StrictHostKeyChecking=yes" +
		" -o IdentityAgent=none -o IdentitiesOnly=yes -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -i " + phoneKey + " \"$@\"\n"
	if err := os.WriteFile(sshWrap, []byte(wrap), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	hubCfg := "[hosts.studio]\naddr = \"" + me.Username + "@127.0.0.1\"\nconnect_timeout = 10\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(hubCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TUIOS_SSH=" + sshWrap}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "studio") && strings.Contains(s, "up")
	}, "the hub never reported studio up through sshd")
	return &pairedLink{base: base, remote: remote, ak: ak, own: own, sshWrap: sshWrap, env: env}
}

func (p *pairedLink) sshLogsIn(t *testing.T) (bool, string) {
	t.Helper()
	me, _ := user.Current()
	cmd := exec.Command(p.sshWrap, "-T", me.Username+"@127.0.0.1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var sshErr lockedBuf
	cmd.Stderr = &sshErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		first <- line
		_, _ = io.Copy(io.Discard, stdout)
	}()
	var line string
	select {
	case line = <-first:
	case <-time.After(20 * time.Second):
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return strings.TrimSpace(line) == "TUIOS-LINK 1", line + sshErr.String()
}

const pinnedRefusal = "has no [hosts.phone] table"

// TestRemovedPairedDeviceLosesItsLink pairs a phone, then removes it the way
// an older tuios did (the table only), restarts the far daemon, and runs
// tuios hosts remove on the key line that is left.
func TestRemovedPairedDeviceLosesItsLink(t *testing.T) {
	p := setupPairedLink(t, "phone")
	var tr transcript
	cfgPath := filepath.Join(xdgDir(p.remote, "XDG_CONFIG_HOME"), "tuios", "config.toml")

	out, err := tuiosCLIEnv(t, p.base, p.env, "list-windows", "-s", "studio:far")
	tr.add("before, list-windows -s studio:far: %v\n%s", err, out)
	if err != nil {
		t.Fatalf("a listing over the paired key was refused before the remove: %v\n%s", err, out)
	}

	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(cfg), "[hosts.phone]")
	if start < 0 {
		t.Fatalf("pair wrote no [hosts.phone] table:\n%s", cfg)
	}
	rest := string(cfg[start+1:])
	end := len(cfg)
	if i := strings.Index(rest, "\n["); i >= 0 {
		end = start + 1 + i + 1
	}
	stripped := string(cfg[:start]) + string(cfg[end:])
	if err := os.WriteFile(cfgPath, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	tr.add("config.toml with the table removed by hand:\n%s", stripped)
	hot := waitTextOf(t, "the open link still lists panes after its table went", 20*time.Second, func() string {
		out, err := tuiosCLIEnv(t, p.base, p.env, "list-windows", "-s", "studio:far")
		if err == nil {
			return "allowed: " + out
		}
		return out
	}, func(s string) bool { return contains(strings.Join(strings.Fields(s), " "), pinnedRefusal) })
	tr.add("hot, list-windows -s studio:far:\n%s", hot)

	if out, err := tuiosCLI(t, p.remote, "kill-server"); err != nil {
		t.Fatalf("kill the far daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, p.remote, "new", "again", "--detach"); err != nil {
		t.Fatalf("start the far daemon again: %v\n%s", err, out)
	}
	after := waitTextOf(t, "after a restart the removed device is not refused", 30*time.Second, func() string {
		out, err := tuiosCLIEnv(t, p.base, p.env, "send-text", "-s", "studio:again", "echo should-not-run")
		if err == nil {
			return "allowed: " + out
		}
		return out
	}, func(s string) bool {
		return strings.HasPrefix(s, "allowed: ") || contains(strings.Join(strings.Fields(s), " "), pinnedRefusal)
	})
	tr.add("after restart, send-text -s studio:again:\n%s", after)
	if strings.HasPrefix(after, "allowed: ") {
		t.Errorf("ASSERTION: the removed device typed into a pane after the restart:\n%s", after)
	}

	out, err = tuiosCLI(t, p.remote, "hosts", "remove", "phone")
	tr.add("hosts remove phone: %v\n%s", err, out)
	if err != nil {
		t.Errorf("ASSERTION: hosts remove of a device with only its key line left failed: %v\n%s", err, out)
	} else if !contains(out, "ssh key of phone") {
		t.Errorf("hosts remove did not say the key went:\n%s", out)
	}
	data, _ := os.ReadFile(p.ak)
	tr.add("authorized_keys:\n%s", data)
	if string(data) != p.own {
		t.Errorf("ASSERTION: authorized_keys is not the person's own key alone:\n%s", data)
	}
	if info, err := os.Stat(p.ak); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("authorized_keys mode: %v %v, want 0600", info.Mode(), err)
	}
	if ok, said := p.sshLogsIn(t); ok {
		t.Errorf("ASSERTION: the removed key still opens the link:\n%s", said)
	} else {
		tr.add("new ssh login with the removed key:\n%s", said)
	}
	tr.save(t, "pair-remove.txt")
}

// TestHostRemoveKeepsTheTableWhenTheKeyStays: when tuios cannot read or
// replace authorized_keys, hosts remove fails, keeps the [hosts.NAME] table,
// and leaves the file whole.
func TestHostRemoveKeepsTheTableWhenTheKeyStays(t *testing.T) {
	needTools(t, "ssh-keygen")
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 000")
	}
	remote := remoteMachine(t)
	keys := filepath.Join(remote, "keys")
	mustMkdir(keys)
	_, buildLine := keygen(t, keys, "build", "-t", "ed25519")
	var own strings.Builder
	for i := range 4 {
		_, l := keygen(t, keys, "own"+strconv.Itoa(i), "-t", "ed25519")
		own.WriteString(l)
		own.WriteByte('\n')
	}
	sshDir := filepath.Join(xdgDir(remote, "HOME"), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ak := filepath.Join(sshDir, "authorized_keys")
	whole := buildLine + " tuios-host=build\n" + own.String()
	if err := os.WriteFile(ak, []byte(whole), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(xdgDir(remote, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[hosts.build]\naddr = \"build.invalid\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var tr transcript
	t.Cleanup(func() { _ = os.Chmod(sshDir, 0o700); _ = os.Chmod(ak, 0o600) })

	refused := func(why string) {
		t.Helper()
		out, err := tuiosCLI(t, remote, "hosts", "remove", "build")
		tr.add("%s: hosts remove build: %v\n%s", why, err, out)
		if err == nil {
			t.Errorf("ASSERTION: %s: hosts remove reported success:\n%s", why, out)
		} else if !contains(out, "cannot remove the ssh key of build") {
			t.Errorf("%s: the error does not say what failed:\n%s", why, out)
		}
		if cfg, _ := os.ReadFile(cfgPath); !contains(string(cfg), "[hosts.build]") {
			t.Errorf("ASSERTION: %s: the table went while the key stayed:\n%s", why, cfg)
		}
	}

	if err := os.Chmod(ak, 0); err != nil {
		t.Fatal(err)
	}
	refused("authorized_keys mode 000")
	if err := os.Chmod(ak, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(sshDir, 0o500); err != nil {
		t.Fatal(err)
	}
	refused("~/.ssh mode 0500")
	if err := os.Chmod(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(ak); string(data) != whole {
		t.Errorf("ASSERTION: a failed rewrite changed authorized_keys:\n%s", data)
	}

	out, err := tuiosCLI(t, remote, "hosts", "remove", "build")
	tr.add("hosts remove build: %v\n%s", err, out)
	if err != nil {
		t.Fatalf("hosts remove build: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(ak); string(data) != own.String() {
		t.Errorf("ASSERTION: authorized_keys is not the own keys alone:\n%s", data)
	}
	tr.save(t, "hosts-remove-keys.txt")
}
