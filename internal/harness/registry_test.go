package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundledManifestsLoad checks every manifest compiled into the binary is
// valid, since a broken one would otherwise only be noticed when a harness
// stopped being recognised.
func TestBundledManifestsLoad(t *testing.T) {
	r, errs := Load()
	for _, e := range errs {
		t.Errorf("bundled manifest %s failed to load: %v", e.Source, e.Err)
	}
	if len(r.IDs()) == 0 {
		t.Fatal("no bundled manifests loaded")
	}
	if r.Lookup("claude-code") == nil {
		t.Error("claude-code is not in the bundled registry")
	}
}

// TestBundledScreenRulesShipOnlyForNeedsInput is the policy check, narrowed from
// "nothing ships enabled" and argued for on purpose.
//
// The old rule cost more than it saved. Reading another program's UI is a
// maintenance treadmill, but a harness waiting on a human was measured emitting
// nothing at all: no output, no title, no progress sequence. With rules off, the
// pane goes quiet, the stall timer calls it idle and the alert policy ignores
// idle, so the state a user most needs to be told about was the one state
// nothing could reach. Off-by-default made that unreachable for everyone who had
// not hand-written rules, which is everyone.
//
// What the treadmill actually costs is bounded in the safe direction: a rule
// that rots stops matching, classification returns no opinion, and the pane
// behaves exactly as it did before the rule existed. The expensive direction is a
// rule matching something it should not, so bundled rules carry several strings
// together rather than any one of them.
//
// working stays off, and that is the part still worth policing. It is already
// carried by OSC 9;4 and by output arriving at all, so a screen rule for it buys
// nothing and would be keyed on a spinner glyph, which is the first thing to
// change in a patch release.
func TestBundledScreenRulesShipOnlyForNeedsInput(t *testing.T) {
	r, _ := Load()
	for _, id := range r.IDs() {
		m := r.Lookup(id)
		if !m.Screen.Enabled {
			continue
		}
		for i, rule := range m.Screen.Rule {
			if rule.State != "needs_input" {
				t.Errorf("bundled manifest %q ships rule %d enabled for state %q; only needs_input may ship on",
					id, i, rule.State)
			}
			if len(rule.All) == 0 && len(rule.Any) < 2 {
				t.Errorf("bundled manifest %q rule %d rests on a single string; a bundled rule needs corroboration",
					id, i)
			}
		}
	}
}

// TestIdentify checks the shapes a harness launches in resolve to the right id,
// and that unrelated programs resolve to none.
func TestIdentify(t *testing.T) {
	r, _ := Load()
	cases := []struct {
		name string
		comm string
		argv []string
		exe  string
		want string
	}{
		{"native claude", "claude", []string{"claude"}, "", "claude-code"},
		{
			"claude renamed over a versioned binary",
			"2.1.222", []string{"claude", "--resume"},
			"/home/u/.local/share/claude/versions/2.1.222",
			"claude-code",
		},
		{
			"claude from npm",
			"node", []string{"node", "/n/node_modules/@anthropic-ai/claude-code/cli.js"}, "",
			"claude-code",
		},
		{"codex native", "codex", []string{"codex"}, "", "codex"},
		{
			"codex from the npm shim",
			"node", []string{"node", "/n/node_modules/@openai/codex/bin/codex.js"}, "",
			"codex",
		},
		{"gemini", "gemini", []string{"gemini"}, "", "gemini-cli"},
		{"opencode", "opencode", []string{"opencode"}, "", "opencode"},
		{"droid via platform package", "droid", []string{"droid"}, "", "droid"},
		{"plain shell", "bash", []string{"-bash"}, "/usr/bin/bash", ""},
		{"unrelated tool", "htop", []string{"htop"}, "/usr/bin/htop", ""},
		{"nothing at all", "", nil, "", ""},
	}
	for _, c := range cases {
		got, ok := r.Identify(c.comm, c.argv, c.exe)
		if c.want == "" {
			if ok {
				t.Errorf("%s: identified as %q, want no harness", c.name, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%s: identified as %q (%v), want %q", c.name, got, ok, c.want)
		}
	}
}

// TestUserManifestLoadsWithoutRebuild is the point of the whole package: a
// harness that did not exist when tuios was built is recognised because a file
// appeared in the user's directory.
func TestUserManifestLoadsWithoutRebuild(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "brandnew.toml", `
schema_version = 1
id             = "brandnew"
display_name   = "Brand New Agent"

[detect]
comm      = ["brandnew"]
argv0     = ["brandnew"]
argv_path = ["@vendor/brandnew"]
`)

	r, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if got, ok := r.Identify("brandnew", []string{"brandnew"}, ""); !ok || got != "brandnew" {
		t.Fatalf("user manifest not used: identified %q (%v)", got, ok)
	}
	// And the bundled ones still work.
	if got, _ := r.Identify("claude", []string{"claude"}, ""); got != "claude-code" {
		t.Errorf("user manifest displaced the bundled ones: claude identified as %q", got)
	}
}

// TestUserManifestReplacesBundled checks a user file wins over the bundled
// manifest with the same id, whole file rather than merged.
func TestUserManifestReplacesBundled(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "claude-code.toml", `
schema_version = 1
id             = "claude-code"
display_name   = "My Claude"

[detect]
comm  = ["mysteryclaude"]
argv0 = ["mysteryclaude"]
`)

	r, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if m := r.Lookup("claude-code"); m == nil || m.DisplayName != "My Claude" {
		t.Fatalf("bundled claude-code was not replaced: %+v", m)
	}
	if got, ok := r.Identify("mysteryclaude", []string{"mysteryclaude"}, ""); !ok || got != "claude-code" {
		t.Errorf("replacement manifest does not match: %q (%v)", got, ok)
	}
	// The replaced predicates are gone, not merged with the bundled ones.
	if _, ok := r.Identify("claude", []string{"claude"}, ""); ok {
		t.Error("replacement merged with the bundled manifest instead of replacing it")
	}
}

// TestBadManifestIsReportedNotSkipped checks a broken file is named in an error
// and does not take the rest of the registry down with it.
func TestBadManifestIsReportedNotSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "wrongversion.toml", `
schema_version = 99
id             = "wrongversion"
[detect]
comm = ["wrongversion"]
`)
	write(t, dir, "generic.toml", `
schema_version = 1
id             = "generic"
[detect]
comm = ["pi"]
`)
	write(t, dir, "empty.toml", `
schema_version = 1
id             = "empty"
[detect]
`)
	write(t, dir, "badstate.toml", `
schema_version = 1
id             = "badstate"
[detect]
comm = ["badstate"]
[[screen.rule]]
state = "confused"
any   = ["what"]
`)
	write(t, dir, "fine.toml", `
schema_version = 1
id             = "fine"
[detect]
comm = ["finetool"]
`)

	r, errs := Load(dir)
	if len(errs) != 4 {
		t.Fatalf("got %d load errors, want 4: %v", len(errs), errs)
	}
	joined := ""
	for _, e := range errs {
		joined += e.Source + ": " + e.Err.Error() + "\n"
	}
	for _, want := range []string{"schema_version", "generic name", "matches nothing", "unknown state"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors do not mention %q:\n%s", want, joined)
		}
	}
	// The good file in the same directory still loaded.
	if r.Lookup("fine") == nil {
		t.Error("a valid manifest was lost because a sibling was broken")
	}
	if r.Lookup("claude-code") == nil {
		t.Error("bundled manifests were lost because a user file was broken")
	}
}

// TestUserDirFollowsXDG checks the directory a user drops manifests into.
func TestUserDirFollowsXDG(t *testing.T) {
	t.Setenv("TUIOS_HARNESS_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := UserDir(), filepath.Join("/xdg", "tuios", "harnesses"); got != want {
		t.Errorf("UserDir = %q, want %q", got, want)
	}
	t.Setenv("TUIOS_HARNESS_DIR", "/override")
	if got := UserDir(); got != "/override" {
		t.Errorf("UserDir with an override = %q, want /override", got)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
