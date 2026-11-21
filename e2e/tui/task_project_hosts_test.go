package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type projectTasks struct {
	Project string `json:"project"`
	Tasks   []struct {
		Host    string `json:"host"`
		Session string `json:"session"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		Stage   string `json:"stage"`
		Project string `json:"project"`
		Holder  *struct {
			Window string `json:"window"`
			Host   string `json:"host"`
			Name   string `json:"name"`
		} `json:"holder"`
	} `json:"tasks"`
	HostsDown []map[string]string `json:"hosts_down"`
}

func (p projectTasks) keys() []string {
	var out []string
	for _, r := range p.Tasks {
		out = append(out, r.Host+"/"+r.Session+"/"+r.ID)
	}
	return out
}

func listProject(t *testing.T, base, project string, hosts bool) projectTasks {
	t.Helper()
	args := []string{"task", "ls", "--project", project, "--json"}
	if hosts {
		args = append(args, "--hosts")
	}
	out := mustCLI(t, base, args...)
	var l projectTasks
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("task ls --project --json: %v\n%s", err, out)
	}
	return l
}

func cliInPane(t *testing.T, base, session, pane, name string, args ...string) (string, int) {
	t.Helper()
	out := filepath.Join(base, name+".out")
	_ = os.Remove(out)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = posixQuote(a)
	}
	line := posixQuote(tuiosBin) + " " + strings.Join(quoted, " ") + " > " + out + ".tmp 2>&1; echo EXIT=$? >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
	mustCLI(t, base, "send-text", "-s", session, "-w", pane, line)
	got := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the pane never finished "+strings.Join(args, " "))
	body, status, _ := strings.Cut(got, "EXIT=")
	code := 0
	fmt.Sscanf(strings.TrimSpace(status), "%d", &code)
	return strings.TrimSpace(body), code
}

func TestProjectCardsAreClaimedFromAnotherMachine(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	remote := remoteMachine(t)
	var report strings.Builder
	note := func(format string, args ...any) {
		fmt.Fprintf(&report, format+"\n", args...)
	}

	startLinkedPair(t, base, remote, "far", "far2")

	mustCLI(t, remote, "task", "add", "-s", "far", "--project", "git@example.com:acme/api.git", "Fix the retry")
	mustCLI(t, remote, "task", "add", "-s", "far", "Only for far")
	mustCLI(t, remote, "task", "add", "-s", "far2", "--project", "https://example.com/acme/api", "Write the docs")
	mustCLI(t, base, "task", "add", "-s", "home", "--project", "ssh://git@example.com/acme/api", "Home card")
	if out, err := tuiosCLI(t, base, "task", "add", "-s", "home", "--project", "/srv/acme/api", "A path"); err == nil {
		t.Errorf("ASSERTION: a local path was taken as a project:\n%s", out)
	}

	const project = "https://example.com/acme/api.git"
	for _, m := range []struct{ name, root, far string }{{"home", base, "build"}, {"build", remote, "home"}} {
		l := listProject(t, m.root, project, true)
		keys := l.keys()
		note("%s task ls --project --hosts: %v", m.name, keys)
		if len(l.HostsDown) > 0 {
			t.Errorf("ASSERTION: %s lists %v as down", m.name, l.HostsDown)
		}
		want := map[string]bool{}
		if m.name == "home" {
			want["/home/t1"], want["build/far/t1"], want["build/far2/t1"] = true, true, true
		} else {
			want["/far/t1"], want["/far2/t1"], want["home/home/t1"] = true, true, true
		}
		if len(keys) != len(want) {
			t.Errorf("ASSERTION: %s lists %d cards, want %d once each: %v", m.name, len(keys), len(want), keys)
		}
		for _, k := range keys {
			if !want[k] {
				t.Errorf("ASSERTION: %s lists %s, which it should not (want %v)", m.name, k, want)
			}
		}
	}
	text := mustCLI(t, base, "task", "ls", "--project", project, "--hosts")
	note("home task ls --project --hosts:\n%s", text)
	for _, w := range []string{"HOST", "build", "far2", "Write the docs"} {
		if !strings.Contains(text, w) {
			t.Errorf("ASSERTION: the listing lacks %q:\n%s", w, text)
		}
	}

	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "worker", "--no-focus", "--print-id"))
	other := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "other", "--no-focus", "--print-id"))
	out, code := cliInPane(t, base, "home", worker, "claim", "task", "claim", "t1", "-s", "build:far")
	note("worker claim build:far t1: exit %d\n%s", code, out)
	if code != 0 || !strings.HasPrefix(out, "t1 Fix the retry") {
		t.Fatalf("ASSERTION: the claim from home failed: exit %d\n%s", code, out)
	}
	held := func(why string) (window, host string) {
		t.Helper()
		for _, r := range listProject(t, remote, project, false).Tasks {
			if r.Session == "far" && r.ID == "t1" {
				if r.Holder == nil {
					return "", ""
				}
				return r.Holder.Window, r.Holder.Host
			}
		}
		t.Fatalf("far t1 is gone (%s)", why)
		return "", ""
	}
	if w, h := held("after the claim"); w != worker || h == "" {
		t.Fatalf("ASSERTION: build says far t1 is held by %q on %q, want window %s on home's link name", w, h, worker)
	}

	out, code = cliInPane(t, base, "home", worker, "claim-again", "task", "claim", "t1", "-s", "build:far")
	note("worker claims again: exit %d\n%s", code, out)
	if code != 0 {
		t.Errorf("ASSERTION: a retried claim by the holder was refused: %s", out)
	}
	out, code = cliInPane(t, base, "home", other, "steal", "task", "claim", "t1", "-s", "build:far")
	note("other pane claims: exit %d\n%s", code, out)
	if code == 0 || !strings.Contains(out, "held") {
		t.Errorf("ASSERTION: a second pane took a held card: %s", out)
	}
	if strings.Contains(out, "did not answer") || strings.Contains(out, "@") {
		t.Errorf("ASSERTION: the refusal reads as a lost answer, or names the holder by its key: %s", out)
	}
	out, code = cliInPane(t, base, "home", other, "steal-release", "task", "release", "t1", "-s", "build:far")
	note("other pane releases: exit %d\n%s", code, out)
	if code == 0 {
		t.Errorf("ASSERTION: a pane that does not hold the card let it go: %s", out)
	}
	if w, _ := held("after the refusals"); w != worker {
		t.Fatalf("ASSERTION: far t1 moved to %q after the refusals", w)
	}
	out, code = cliInPane(t, base, "home", other, "session-card", "task", "claim", "t2", "-s", "build:far")
	note("other pane claims the session card: exit %d\n%s", code, out)
	if code == 0 || !strings.Contains(out, "only") {
		t.Errorf("ASSERTION: a session card was claimed from another machine: %s", out)
	}
	out, code = cliInPane(t, base, "home", other, "far2", "task", "claim", "t1", "-s", "build:far2")
	note("other pane claims build:far2 t1: exit %d\n%s", code, out)
	if code != 0 || !strings.Contains(out, "Write the docs") {
		t.Errorf("ASSERTION: the claim of far2's t1 failed or took another card: %s", out)
	}

	breakTheLink(t)
	out, code = cliInPane(t, base, "home", worker, "claim-down", "task", "release", "t1", "-s", "build:far")
	note("worker release with the link down: exit %d\n%s", code, out)
	wantClaims := 1
	if code == 0 {
		note("the link was back before the call")
		wantClaims = 2
	} else if !strings.Contains(out, "again") {
		t.Errorf("ASSERTION: the failed call does not say to run it again: %s", out)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		out, code = cliInPane(t, base, "home", worker, "claim-back", "task", "claim", "t1", "-s", "build:far")
		if code == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	note("worker claims after the link is back: exit %d\n%s", code, out)
	if code != 0 {
		t.Fatalf("ASSERTION: the claim after the link came back failed: %s", out)
	}

	mustCLI(t, base, "close-window", "-s", "home", worker)
	deadline = time.Now().Add(uiTimeout)
	for {
		if w, _ := held("after the pane closed"); w == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: far t1 is still held after its pane on home closed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	hist := mustCLI(t, remote, "task", "get", "-s", "far", "t1")
	note("build task get far t1:\n%s", hist)
	if got := strings.Count(hist, "claimed"); got != wantClaims {
		t.Errorf("ASSERTION: far t1's history holds %d claims, want %d (a retry by the holder adds none):\n%s", got, wantClaims, hist)
	}
	saveText(t, "task-project-hosts", report.String())
}

func startLinkedPair(t *testing.T, base, remote string, sessions ...string) {
	t.Helper()
	writeHosts := func(root, name, peer string) []string {
		ssh := writeFakeSSHTo(t, root, peer)
		dir := filepath.Join(xdgDir(root, "XDG_CONFIG_HOME"), "tuios")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := "[hosts." + name + "]\naddr = \"someone@" + name + "box\"\ncommand = \"" + tuiosBin + "\"\nconnect_timeout = 5\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		return []string{"TUIOS_SSH=" + ssh}
	}
	homeEnv := writeHosts(base, "build", remote)
	buildEnv := writeHosts(remote, "home", base)
	if out, err := tuiosCLIEnv(t, base, homeEnv, "new", "home", "--detach"); err != nil {
		t.Fatalf("start home's daemon: %v\n%s", err, out)
	}
	for _, s := range sessions {
		if out, err := tuiosCLIEnv(t, remote, buildEnv, "new", s, "--detach"); err != nil {
			t.Fatalf("start build's session %s: %v\n%s", s, err, out)
		}
	}
	for _, root := range []string{base, remote} {
		deadline := time.Now().Add(45 * time.Second)
		for len(listProject(t, root, "https://example.com/x/y", true).HostsDown) > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: the links never came up: %s", mustCLI(t, root, "hosts"))
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

}
