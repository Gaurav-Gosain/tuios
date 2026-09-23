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

// TestBundledScreenRulesPolicy is the policy check, narrowed from
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
// Working rules ship for every harness that has chrome to key on, so an
// unhooked pane can say it is busy rather than drifting to unknown on the
// silence timer. Idle ships only for the harnesses in screenIdleHarnesses:
// the ones whose idle screen was measured here, or for which herdr ships an
// idle rule from its own live pane reads. Three things keep that honest. An
// idle rule has to prove an input box is on the screen (the loader refuses one
// that does not), every idle rule is outranked by every working and
// needs_input rule of its manifest, so a screen showing the box and a live
// turn at once reads as the louder state, and the daemon holds an idle
// verdict through a confirmation window before it publishes it.
func TestBundledScreenRulesPolicy(t *testing.T) {
	screenIdleHarnesses := map[string]bool{
		"claude-code": true, "codex": true, "gemini-cli": true, "opencode": true,
		"cline": true, "devin": true, "grok": true, "kiro": true, "maki": true, "qwen": true,
	}
	r, _ := Load()
	for _, id := range r.IDs() {
		m := r.Lookup(id)
		if !m.Screen.Enabled {
			continue
		}
		minLoud := 1 << 30
		for _, rule := range m.Screen.Rule {
			if rule.State != "idle" && rule.Priority < minLoud {
				minLoud = rule.Priority
			}
		}
		for i, rule := range m.Screen.Rule {
			if rule.State == "idle" && !screenIdleHarnesses[id] {
				t.Errorf("bundled manifest %q ships idle rule %d; idle ships only where it was measured or herdr ships it",
					id, i)
			}
			if rule.State == "idle" && rule.Priority >= minLoud {
				t.Errorf("bundled manifest %q idle rule %d has priority %d, not below every louder rule (%d)",
					id, i, rule.Priority, minLoud)
			}
			if !corroborated(&rule.Gate) {
				t.Errorf("bundled manifest %q rule %d rests on a single string; a bundled rule needs corroboration",
					id, i)
			}
		}
	}
}

// corroborated reports whether a gate needs more than one loose substring to
// match. A pattern counts on its own: it pins the structure of a rendered line,
// which is harder to meet by accident than any one substring. An all[] string
// counts, as it always has, because it is a named phrase the screen must carry
// rather than one of several accepted. A nested group counts when every path
// through it does.
func corroborated(g *Gate) bool {
	if len(g.Regex) > 0 || len(g.All) > 0 || len(g.Any) >= 2 {
		return true
	}
	for i := range g.AllOf {
		if corroborated(&g.AllOf[i]) {
			return true
		}
	}
	if len(g.AnyOf) == 0 {
		return false
	}
	for i := range g.AnyOf {
		if !corroborated(&g.AnyOf[i]) {
			return false
		}
	}
	return true
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
	var joined strings.Builder
	for _, e := range errs {
		joined.WriteString(e.Source + ": " + e.Err.Error() + "\n")
	}
	for _, want := range []string{"schema_version", "generic name", "matches nothing", "unknown state"} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("errors do not mention %q:\n%s", want, joined.String())
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

// TestResolveTakesAnIdOrTheProgramName pins the two spellings a person uses
// for an agent: the manifest id and the name they type at a shell.
func TestResolveTakesAnIdOrTheProgramName(t *testing.T) {
	reg, _ := Load()
	for _, name := range []string{"claude-code", "claude"} {
		m, cmd, ok := reg.Resolve(name)
		if !ok || m == nil {
			t.Fatalf("Resolve(%q) found nothing", name)
		}
		if m.ID != "claude-code" || cmd != "claude" {
			t.Errorf("Resolve(%q) = %s, %q; want claude-code, claude", name, m.ID, cmd)
		}
	}
	if _, _, ok := reg.Resolve("not-an-agent"); ok {
		t.Error("Resolve accepted a name no manifest knows")
	}
	if _, _, ok := reg.Resolve(""); ok {
		t.Error("Resolve accepted an empty name")
	}
}
