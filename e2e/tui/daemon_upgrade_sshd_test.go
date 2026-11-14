package tuie2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A link to a different machine over real ssh, across `tuios daemon upgrade`.
//
// TestFederationLinkRedialsAfterUpgrade links the daemon to itself through
// an ssh stand-in, so the far end of its link is a client of the daemon being
// upgraded, and the upgrade's closing of every client ends the link's ssh
// before the exec. A link to another machine does not end that way: its ssh
// child is still running when the old image execs the new one. These tests
// give the link a far end of its own, a Docker container running sshd and a
// Linux build of this checkout with its own daemon, and reach it with the
// real ssh client.
//
// Opt in with TUIOS_E2E_SSHD=1. The first run builds the image from
// testdata/sshd, which needs the network for one apk install; later runs use
// Docker's cache. TUIOS_E2E_SSHD_IMAGE renames the image, and the containers,
// which are named after it. Nothing touches the person's ~/.ssh: the client
// runs with -F and a config, key and known_hosts file of the test's own.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff", PR 5).

// sshdHost is a container another daemon links to over ssh.
type sshdHost struct {
	// container is the Docker container's name.
	container string
	// addr is the ssh address of the container, an alias in config.
	addr string
	// config is the ssh client config that reaches it, and ssh a program
	// that runs the real ssh with it, for TUIOS_SSH.
	config, ssh string
}

// sshdDocker runs docker with a deadline and returns its combined output.
func sshdDocker(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// remoteSSHHost starts a container with sshd and a daemon of a Linux build
// of this checkout, and returns how to reach it. It skips when the test was
// not asked for, or when this machine cannot run it.
func remoteSSHHost(t *testing.T) sshdHost {
	t.Helper()
	if os.Getenv("TUIOS_E2E_SSHD") == "" {
		t.Skip("set TUIOS_E2E_SSHD=1 to link to a Docker container over real ssh")
	}
	for _, tool := range []string{"docker", "ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on the PATH: %v", tool, err)
		}
	}
	server, err := sshdDocker(20*time.Second, "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")
	if err != nil {
		t.Skipf("no Docker engine answers: %v: %s", err, server)
	}
	goos, goarch, _ := strings.Cut(server, "/")
	if goos != "linux" {
		t.Skipf("the Docker engine runs %s containers, and the image is Linux", server)
	}
	sshPath, _ := exec.LookPath("ssh")

	dir := t.TempDir()
	bin := filepath.Join(dir, "tuios-"+goos+"-"+goarch)
	build := exec.Command("go", "build", "-o", bin, "./cmd/tuios")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tuios for %s: %v\n%s", server, err, out)
	}

	image := "tuios-e2e-sshd"
	if s := os.Getenv("TUIOS_E2E_SSHD_IMAGE"); s != "" {
		image = s
	}
	if out, err := sshdDocker(10*time.Minute, "build", "-q", "-t", image, "testdata/sshd"); err != nil {
		t.Fatalf("docker build %s: %v\n%s", image, err, out)
	}

	key := filepath.Join(dir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "tuios-e2e", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}

	name := image + "-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10)
	if out, err := sshdDocker(time.Minute, "create", "--name", name, "--cpus", "2", "-p", "127.0.0.1::22", image); err != nil {
		t.Fatalf("docker create: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := sshdDocker(time.Minute, "rm", "-f", name); err != nil {
			t.Logf("docker rm %s: %v: %s", name, err, out)
		}
	})
	for _, step := range [][]string{
		{"cp", bin, name + ":/usr/local/bin/tuios"},
		{"cp", key + ".pub", name + ":/home/tuios/.ssh/authorized_keys"},
		{"start", name},
		{"exec", name, "sh", "-c", "chmod 755 /usr/local/bin/tuios && chown tuios:tuios /home/tuios/.ssh/authorized_keys && chmod 600 /home/tuios/.ssh/authorized_keys"},
		{"exec", "-u", "tuios", "-e", "HOME=/home/tuios", name, "tuios", "start-server"},
	} {
		if out, err := sshdDocker(2*time.Minute, step...); err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(step, " "), err, out)
		}
	}
	ports, err := sshdDocker(time.Minute, "port", name, "22/tcp")
	if err != nil {
		t.Fatalf("docker port: %v\n%s", err, ports)
	}
	var port string
	for _, line := range strings.Split(ports, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "127.0.0.1:"); ok {
			port = p
		}
	}
	if port == "" {
		t.Fatalf("the container publishes sshd on no 127.0.0.1 port: %q", ports)
	}

	h := sshdHost{container: name, addr: "tuios-e2e-far", config: filepath.Join(dir, "ssh_config"), ssh: filepath.Join(dir, "ssh")}
	config := "Host " + h.addr + "\n" +
		"  HostName 127.0.0.1\n" +
		"  Port " + port + "\n" +
		"  User tuios\n" +
		"  IdentityFile " + key + "\n" +
		"  IdentitiesOnly yes\n" +
		"  IdentityAgent none\n" +
		"  UserKnownHostsFile " + filepath.Join(dir, "known_hosts") + "\n" +
		"  GlobalKnownHostsFile /dev/null\n" +
		"  StrictHostKeyChecking accept-new\n" +
		"  LogLevel ERROR\n"
	if err := os.WriteFile(h.config, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// exec, so the daemon's child is ssh itself and its command line names
	// the config, which is how the tests find it among the daemon's children.
	if err := os.WriteFile(h.ssh, []byte("#!/bin/sh\nexec "+sshPath+" -F "+h.config+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := exec.Command(h.ssh, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", h.addr, "tuios ls").CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ssh to the container never worked: %v\n%s", err, out)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return h
}

// linkChildren reads the daemon's children: the ssh processes that carry a
// link to h, and every defunct child, whatever it ran. A defunct child's
// command line is gone, so it cannot be told apart, and the daemon should
// have none.
func (h sshdHost) linkChildren(t *testing.T, pid int) (live, defunct, all []string) {
	t.Helper()
	all = childCommands(t, pid)
	for _, c := range all {
		switch {
		case strings.Contains(c, "<defunct>"):
			defunct = append(defunct, c)
		// ssh -G is the short-lived question the link asks before it dials.
		case strings.Contains(c, h.config) && !strings.Contains(c, " -G "):
			live = append(live, c)
		}
	}
	return
}

// farProxies lists the stdio-proxy processes in the container: one per link
// that reaches its daemon.
func (h sshdHost) farProxies(t *testing.T) []string {
	t.Helper()
	out, err := sshdDocker(30*time.Second, "exec", h.container, "ps", "-o", "pid,args")
	if err != nil {
		t.Fatalf("ps in the container: %v\n%s", err, out)
	}
	var proxies []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "stdio-proxy") && !strings.Contains(line, "ps -o") {
			proxies = append(proxies, strings.TrimSpace(line))
		}
	}
	return proxies
}

// childPID is the pid at the front of a childCommands entry.
func childPID(entry string) string {
	pid, _, _ := strings.Cut(entry, " ")
	return pid
}

// TestFederationLinkToAnotherMachineAfterUpgrade upgrades a daemon whose link
// reaches another machine over real ssh. After the upgrade the link is up, the
// daemon has one live ssh child for it and no defunct child, the old image's
// ssh is gone, the far machine serves one link and not two, and the local
// pane kept its process.
func TestFederationLinkToAnotherMachineAfterUpgrade(t *testing.T) {
	far := remoteSSHHost(t)
	base := t.TempDir()
	env := []string{"TUIOS_SSH=" + far.ssh}
	startHandoffDaemon(t, base, env...)
	second := copyTuios(t)
	const sess = "e2e-sshd"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	if out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "far", far.addr, "--connect-timeout", "5"); err != nil {
		t.Fatalf("hosts add: %v\n%s", err, out)
	}
	up := func(s string) bool { return strings.Contains(s, "far") && strings.Contains(s, "up") }
	waitForHostListing(t, base, up, "the link to the container never came up before the upgrade")
	img := helloImage(t, base)
	panesBefore := panePIDs(t, base, sess)
	liveBefore, defunctBefore, allBefore := far.linkChildren(t, img.PID)
	proxiesBefore := far.farProxies(t)
	if len(liveBefore) != 1 || len(defunctBefore) != 0 {
		t.Fatalf("before the upgrade the daemon has %d live ssh children for the link and %d defunct children, want 1 and 0: %v", len(liveBefore), len(defunctBefore), allBefore)
	}

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
	}
	after := helloImage(t, base)
	listing := waitForHostListing(t, base, up, "the link to the container did not come back up after the upgrade")

	// The old ssh has the old image's pipes closed under it by the exec, and
	// may take a moment to notice and exit. Exiting is not enough: a child
	// nobody waits for stays defunct. So the wait is for the end state, and
	// a wrong one is reported as it stood at the deadline.
	var live, defunct, all, proxies []string
	deadline := time.Now().Add(10 * time.Second)
	for {
		live, defunct, all = far.linkChildren(t, img.PID)
		proxies = far.farProxies(t)
		settled := len(live) == 1 && len(defunct) == 0 && len(proxies) == 1 && childPID(live[0]) != childPID(liveBefore[0])
		if settled || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	panesAfter := panePIDs(t, base, sess)
	writeHandoffArtifact(t, map[string]any{
		"upgrade": text, "hosts": listing, "container": far.container,
		"pid_before": img.PID, "pid_after": after.PID, "instance_before": img.Instance, "instance_after": after.Instance,
		"children_before": allBefore, "children_after": all,
		"link_children_before": liveBefore, "link_children_after": live, "defunct_children_after": defunct,
		"far_proxies_before": proxiesBefore, "far_proxies_after": proxies,
		"panes_before": panesBefore, "panes_after": panesAfter,
	})

	if after.PID != img.PID || after.Instance == img.Instance {
		t.Errorf("ASSERTION: the daemon is %+v after the upgrade, from %+v; want the same pid on a new image", after, img)
	}
	if len(live) != 1 {
		t.Errorf("ASSERTION: after the upgrade the daemon has %d live ssh children for the link, want 1: %v", len(live), all)
	}
	if len(defunct) != 0 {
		t.Errorf("ASSERTION: after the upgrade the daemon has defunct children nothing waits for: %v", defunct)
	}
	old := childPID(liveBefore[0])
	if slices.ContainsFunc(all, func(c string) bool { return childPID(c) == old }) {
		t.Errorf("ASSERTION: the old image's ssh, pid %s, is still the daemon's child after the upgrade: %v", old, all)
	}
	if len(proxies) != 1 {
		t.Errorf("ASSERTION: the far machine serves %d links after the upgrade, want 1: %v (before: %v)", len(proxies), proxies, proxiesBefore)
	}
	for id, pid := range panesBefore {
		if panesAfter[id] != pid {
			t.Errorf("ASSERTION: window %s had pid %d, and %d after the upgrade", id, pid, panesAfter[id])
		}
	}
}
