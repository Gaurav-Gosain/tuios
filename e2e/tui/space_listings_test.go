package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The two session listings besides the live tuios ls: the saved listing tuios
// ls prints with no daemon, and tuios ls --all-hosts. Both group by space the
// way tuios ls does, and with no session in any space both print what a build
// without the grouping prints.

// spaceGroup is one table a grouped listing should draw: the space heading it,
// "" for the sessions in no space, and the sessions it holds.
type spaceGroup struct {
	heading string
	members []string
}

// wantSpaceGroups fails unless blocks starts with exactly groups, in order:
// each space's table under its name, the sessions in no space with no heading,
// and each table holding only its own sessions out of all.
func wantSpaceGroups(t *testing.T, what string, blocks []string, groups []spaceGroup, all []string, out string) {
	t.Helper()
	if len(blocks) < len(groups) {
		t.Fatalf("ASSERTION: %s has %d blocks, want at least the %d tables:\n%s", what, len(blocks), len(groups), out)
	}
	for i, g := range groups {
		block := blocks[i]
		first, _, _ := strings.Cut(block, "\n")
		if g.heading == "" && !strings.HasPrefix(strings.TrimSpace(first), "╭") {
			t.Fatalf("ASSERTION: in %s the sessions in no space have a heading %q:\n%s", what, first, out)
		}
		if g.heading != "" && strings.TrimSpace(first) != g.heading {
			t.Fatalf("ASSERTION: in %s block %d is headed %q, want %q:\n%s", what, i, first, g.heading, out)
		}
		for _, name := range all {
			if strings.Contains(block, name) != slices.Contains(g.members, name) {
				t.Fatalf("ASSERTION: in %s block %q holds %s wrongly, want exactly %v:\n%s", what, g.heading, name, g.members, out)
			}
		}
	}
}

// savedListing is tuios ls with no daemon running. It exits with the
// no-daemon status, so only the output is kept.
func savedListing(t *testing.T, base string, args ...string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"ls"}, args...)...)
	if err == nil {
		t.Fatalf("ASSERTION: ls with no daemon exited 0, so a daemon answered:\n%s", out)
	}
	return out
}

// TestSavedLsGroupsSessionsBySpace: with no daemon, tuios ls lists the saved
// sessions, and groups them by the space each state file records, as the live
// listing does. ls --json stays one flat list, with space on each saved row
// that has one.
//
// How it could pass wrongly, and what stops it:
//   - a daemon answering instead of the state files: the daemon is stopped,
//     ls must exit with the no-daemon status, and every row must be tagged
//     saved.
//   - headings over one table of every session: each block must hold only its
//     own sessions.
//   - the daemon's or the files' order taken for the grouping: the sessions
//     are made out of name order, with the loose one first.
//   - the space read from the live session rather than the file: nothing is
//     live when ls runs.
//   - the grouping applied to --json too: the JSON must decode as a flat array
//     of rows.
//
// The grouped listing is written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces in the saved and
// --all-hosts listings").
func TestSavedLsGroupsSessionsBySpace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	newInSpace(t, base, "e2e-zulu", "zeta")
	newInSpace(t, base, "e2e-alpha-1", "alpha")
	newInSpace(t, base, "e2e-alpha-2", "alpha")

	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}

	out := savedListing(t, base)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "ls-saved-grouped.txt"), []byte(out), 0o644); err != nil {
		t.Logf("could not save the listing: %v", err)
	}
	all := []string{"e2e-alpha-1", "e2e-alpha-2", "e2e-zulu", "e2e-loose"}
	blocks := lsBlocks(out)
	wantSpaceGroups(t, "the saved listing", blocks, []spaceGroup{
		{"alpha", []string{"e2e-alpha-1", "e2e-alpha-2"}},
		{"zeta", []string{"e2e-zulu"}},
		{"", []string{"e2e-loose"}},
	}, all, out)
	if len(blocks) < 4 || !strings.HasPrefix(blocks[3], "4 sessions") {
		t.Fatalf("ASSERTION: the saved listing's tables are not followed by its count:\n%s", out)
	}
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(out, ""), "\n") {
		for _, name := range all {
			if strings.Contains(line, name) && !strings.Contains(line, "saved") {
				t.Fatalf("ASSERTION: the saved listing does not tag %s saved:\n%s", name, out)
			}
		}
	}

	jsonOut := savedListing(t, base, "--json")
	var rows []struct {
		Name  string `json:"name"`
		Space string `json:"space"`
		Saved bool   `json:"saved"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("ASSERTION: the saved ls --json is not a flat list of sessions: %v\n%s", err, jsonOut)
	}
	got := map[string]string{}
	for _, r := range rows {
		if !r.Saved {
			t.Fatalf("ASSERTION: the saved ls --json lists %s as not saved:\n%s", r.Name, jsonOut)
		}
		got[r.Name] = r.Space
	}
	want := map[string]string{"e2e-loose": "", "e2e-zulu": "zeta", "e2e-alpha-1": "alpha", "e2e-alpha-2": "alpha"}
	if len(got) != len(want) {
		t.Fatalf("ASSERTION: the saved ls --json has the sessions and spaces %v, want %v", got, want)
	}
	for name, space := range want {
		if sp, ok := got[name]; !ok || sp != space {
			t.Fatalf("ASSERTION: the saved ls --json has the sessions and spaces %v, want %v", got, want)
		}
	}
	if strings.Contains(jsonOut, `"space": ""`) {
		t.Fatalf("ASSERTION: the saved ls --json writes an empty space rather than leaving it out:\n%s", jsonOut)
	}
}

// allHostsListing polls tuios ls --all-hosts until every name in want is
// listed, which is when the link to the host has answered.
func allHostsListing(t *testing.T, bin, base string, env []string, want []string, args ...string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLIBinEnv(t, bin, base, env, append([]string{"ls", "--all-hosts"}, args...)...)
		listed := true
		for _, name := range want {
			listed = listed && strings.Contains(out, name)
		}
		if listed {
			return out
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("ls --all-hosts never listed all of %v. The last listing was:\n%s", want, out)
	return out
}

// hostSections splits tuios ls --all-hosts output, styling stripped, into each
// host's blocks, keyed by the host's line.
func hostSections(out string, hosts []string) map[string][]string {
	sections := map[string][]string{}
	current := ""
	for _, block := range lsBlocks(out) {
		first, rest, _ := strings.Cut(block, "\n")
		if slices.Contains(hosts, strings.TrimSpace(first)) {
			current = strings.TrimSpace(first)
			if rest == "" {
				continue
			}
			block = rest
		}
		if current != "" {
			sections[current] = append(sections[current], block)
		}
	}
	return sections
}

// TestAllHostsLsGroupsEachHostBySpace: tuios ls --all-hosts groups each host's
// sessions by space under that host's line, as ls on that host would, and a
// space of the same name on two hosts is two groups. ls --all-hosts --json
// keeps its shape, with space on each session row that has one, the far
// host's included.
//
// How it could pass wrongly, and what stops it:
//   - the far host's space dropped on the way through this daemon: the far
//     sessions are in spaces only the far daemon knows, and its groups must
//     show them.
//   - one set of groups across every host: both hosts have an alpha space,
//     and each host's alpha must hold only that host's session.
//   - headings over one table per host: each block must hold only its own
//     sessions.
//   - the grouping applied to --json: the JSON must decode as the host
//     entries it always was.
//
// The grouped listing is written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces in the saved and
// --all-hosts listings").
func TestAllHostsLsGroupsEachHostBySpace(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)

	if out, err := tuiosCLI(t, remote, "new", "far-loose", "--detach"); err != nil {
		t.Fatalf("create far-loose: %v: %s", err, out)
	}
	newInSpace(t, remote, "far-api", "payments")
	newInSpace(t, remote, "far-alpha", "alpha")

	// The first command starts this machine's daemon, with the ssh stand-in.
	if out, err := tuiosCLIEnv(t, base, env, "new", "near-loose", "--detach"); err != nil {
		t.Fatalf("create near-loose: %v: %s", err, out)
	}
	newInSpace(t, base, "near-alpha", "alpha")

	all := []string{"near-loose", "near-alpha", "far-loose", "far-api", "far-alpha"}
	out := allHostsListing(t, tuiosBin, base, env, all)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "ls-all-hosts-grouped.txt"), []byte(out), 0o644); err != nil {
		t.Logf("could not save the listing: %v", err)
	}
	sections := hostSections(out, []string{"local", "build"})
	wantSpaceGroups(t, "the local host's listing", sections["local"], []spaceGroup{
		{"alpha", []string{"near-alpha"}},
		{"", []string{"near-loose"}},
	}, all, out)
	wantSpaceGroups(t, "the build host's listing", sections["build"], []spaceGroup{
		{"alpha", []string{"far-alpha"}},
		{"payments", []string{"far-api"}},
		{"", []string{"far-loose"}},
	}, all, out)

	jsonOut, err := tuiosCLIEnv(t, base, env, "ls", "--all-hosts", "--json")
	if err != nil {
		t.Fatalf("ls --all-hosts --json: %v: %s", err, jsonOut)
	}
	var res struct {
		Hosts []struct {
			Host     string `json:"host"`
			Sessions []struct {
				Name  string `json:"name"`
				Space string `json:"space"`
			} `json:"sessions"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &res); err != nil {
		t.Fatalf("ASSERTION: ls --all-hosts --json is not the host entries: %v\n%s", err, jsonOut)
	}
	got := map[string]string{}
	for _, h := range res.Hosts {
		for _, s := range h.Sessions {
			got[h.Host+"/"+s.Name] = s.Space
		}
	}
	want := map[string]string{
		"local/near-loose": "", "local/near-alpha": "alpha",
		"build/far-loose": "", "build/far-api": "payments", "build/far-alpha": "alpha",
	}
	for key, space := range want {
		if sp, ok := got[key]; !ok || sp != space {
			t.Fatalf("ASSERTION: ls --all-hosts --json has the sessions and spaces %v, want %v\n%s", got, want, jsonOut)
		}
	}
}

// TestSpacelessListingsAreUnchanged: with no session in any space, the saved
// listing and tuios ls --all-hosts print exactly what a build without the
// grouping prints for the same sessions, byte for byte once the isolation
// roots are named alike. The same fixture is made on daemons of each build,
// and each build's own CLI reads its own daemons.
//
// It needs that other build: name it with TUIOS_E2E_BASELINE_BIN, a tuios
// built from upstream main. Without it the test is skipped.
//
// How it could pass wrongly, and what stops it:
//   - the fixtures differing, so a change in a listing hides behind a change
//     in the data: both are made by the same steps, and each listing must
//     name every session.
//   - a fixture too plain to show a difference: it holds a session with
//     several windows and one with a display name on each host, and the
//     saved listing holds both.
//
// All four listings are written to the test's artifact directory.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Spaces in the saved and
// --all-hosts listings").
func TestSpacelessListingsAreUnchanged(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream main and name it there")
	}
	listings := func(bin string) (allHosts, saved string) {
		base := t.TempDir()
		remote := t.TempDir()
		for _, key := range xdgKeys {
			xdgDir(remote, key)
		}
		ssh := writeFakeSSHTo(t, base, remote)
		writeOneHostConfig(t, base, bin)
		env := []string{"TUIOS_SSH=" + ssh}
		run := func(root string, args ...string) {
			t.Helper()
			out, err := tuiosCLIBinEnv(t, bin, root, env, args...)
			if err != nil {
				t.Fatalf("%s %v: %v: %s", filepath.Base(bin), args, err, out)
			}
		}
		t.Cleanup(func() {
			_, _ = tuiosCLIBinEnv(t, bin, base, nil, "kill-server")
			_, _ = tuiosCLIBinEnv(t, bin, remote, nil, "kill-server")
		})
		for _, root := range []string{remote, base} {
			run(root, "new", "e2e-alpha", "--detach")
			run(root, "new", "e2e-beta", "--detach")
			run(root, "new-window", "-s", "e2e-beta", "--no-focus")
			run(root, "set-session-name", "-s", "e2e-beta", "Beta API")
		}
		allHosts = allHostsListing(t, bin, base, env, []string{"e2e-alpha", "Beta API", "build\n", "4 sessions on 2 hosts"})
		run(base, "kill-server")
		saved, _ = tuiosCLIBinEnv(t, bin, base, nil, "ls")
		for _, name := range []string{"e2e-alpha", "e2e-beta"} {
			if !strings.Contains(saved, name) {
				t.Fatalf("the saved listing of %s does not hold %s:\n%s", filepath.Base(bin), name, saved)
			}
		}
		return strings.ReplaceAll(allHosts, base, "<base>"), strings.ReplaceAll(saved, base, "<base>")
	}
	gotHosts, gotSaved := listings(tuiosBin)
	wantHosts, wantSaved := listings(baseline)
	dir := artifactDir(t)
	_ = os.WriteFile(filepath.Join(dir, "ls-all-hosts-this-build.txt"), []byte(gotHosts), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "ls-all-hosts-baseline.txt"), []byte(wantHosts), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "ls-saved-this-build.txt"), []byte(gotSaved), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "ls-saved-baseline.txt"), []byte(wantSaved), 0o644)
	if gotHosts != wantHosts {
		t.Fatalf("ASSERTION: with no session in a space, ls --all-hosts differs from the baseline build's\nthis build:\n%s\nbaseline:\n%s", gotHosts, wantHosts)
	}
	if gotSaved != wantSaved {
		t.Fatalf("ASSERTION: with no session in a space, the saved ls differs from the baseline build's\nthis build:\n%s\nbaseline:\n%s", gotSaved, wantSaved)
	}
}

// tuiosCLIBinEnv is tuiosCLIWith with extra environment.
func tuiosCLIBinEnv(t *testing.T, bin, base string, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(bin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
