package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestRiskMarksJJCommands holds a Bash approval through Claude Code's real hook
// for each jj command that pushes, rewinds or drops work, and for the jj
// commands that do none of that, and reads the rules the Inbox item carries.
// jj git push moves a rewritten bookmark as --force-with-lease does, jj op
// restore rewinds every workspace, and jj restore with no paths or jj abandon
// drop work. jj takes its global options anywhere, so -R before the
// subcommand must not hide it, and a revset that names a subcommand must not
// match one.
//
// The test fails on a build without the jj rules: the risky commands carry no
// rule there.
func TestRiskMarksJJCommands(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		session, command, rule string
	}{
		{"jj-push", "jj git push -b feat/x", "force push"},
		{"jj-push-r", "jj -R ../api git push --all", "force push"},
		{"jj-op-restore", "jj op restore 3f9a0c2b7d41", "hard reset"},
		{"jj-restore", "jj restore", "discard changes"},
		{"jj-restore-dot", "jj restore -c @- .", "discard changes"},
		{"jj-abandon", "jj abandon -r @-", "discard changes"},
		{"jj-push-dry", "jj git push --dry-run -b feat/x", ""},
		{"jj-fetch", "jj git fetch", ""},
		{"jj-undo", "jj undo", ""},
		{"jj-restore-file", "jj restore --from @-- src/a.go", ""},
		{"jj-log", "jj log -r 'abandon'", ""},
	}
	jjRules := []string{"force push", "hard reset", "discard changes"}
	for _, c := range cases {
		if out, err := tuiosCLI(t, base, "new", c.session, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", c.session, err, out)
		}
	}
	for _, c := range cases {
		startHeldBashHook(t, base, c.session, c.command, "")
	}
	for _, c := range cases {
		it := waitRiskItem(t, base, c.session)
		t.Logf("%s: %q risk %v", c.session, c.command, it.Risk)
		if c.rule != "" && !slices.Contains(it.Risk, c.rule) {
			t.Errorf("ASSERTION: %q is marked %v, want %q", c.command, it.Risk, c.rule)
		}
		if c.rule == "" && slices.ContainsFunc(it.Risk, func(r string) bool { return slices.Contains(jjRules, r) }) {
			t.Errorf("ASSERTION: %q is marked %v, want none of %v", c.command, it.Risk, jjRules)
		}
	}
	saveRiskItems(t, base, "risk-jj-commands-items.json")
}
