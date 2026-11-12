package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationInstallRex: tuios integration install rex puts tuios.lua next
// to Rex's init.lua and adds one marked require line, has Rex check and reload
// the config, and reports what tuios_check found inside Rex. A person's own
// init.lua survives byte for byte, uninstall gives it back as it was, and a
// config Rex refuses leaves both files as they were. A fake rex on PATH
// records every call and answers as Rex does.
//
// How it could pass wrongly, written down first:
//   - Install could rewrite the person's lines while adding its own: their
//     init.lua must be the start of the new file, byte for byte, and
//     uninstall must give back exactly the old bytes.
//   - A refused config could be left in place: the fake check fails on
//     request, and both files must be as before.
//   - A hand-written require could get a second one: init.lua must not
//     change, and status must say the line is the person's.
//   - A tuios.lua of the person's own could be overwritten: install refuses
//     it without --force, and --force keeps it as a .tuios.bak.
//   - The proof could be skipped: the fake log must hold config check,
//     config reload and do tuios_check, in that order.
//   - The installed file could differ from the one the stub tests check:
//     it is compared with integrations/rex/tuios.lua.
//
// NEGATIVE CONTROL: without the rex case in integration install, every case
// fails with "no integration for "rex"".
func TestIntegrationInstallRex(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join("..", "..", "integrations", "rex", "tuios.lua"))
	if err != nil {
		t.Fatal(err)
	}
	type rig struct {
		base, cfg, lua, log string
		env                 []string
	}
	newRig := func(t *testing.T, fail, agentFound bool) rig {
		t.Helper()
		base := t.TempDir()
		bin := filepath.Join(base, "bin")
		cfgDir := filepath.Join(base, "rexcfg")
		for _, d := range []string{bin, cfgDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		r := rig{base: base, cfg: filepath.Join(cfgDir, "init.lua"), lua: filepath.Join(cfgDir, "tuios.lua"), log: filepath.Join(base, "rex.log")}
		found := "true"
		if !agentFound {
			found = "false"
		}
		check := "exit 0"
		if fail {
			check = "echo 'init.lua:3: unexpected symbol near tuios' >&2; exit 1"
		}
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + r.log + "'\n" +
			"case \"$1 $2\" in\n" +
			"'config check') " + check + " ;;\n" +
			"'config reload') exit 0 ;;\n" +
			"esac\n" +
			"case \"$1\" in\n" +
			"actions) grep -q 'require(\"tuios\")' \"$REX_CONFIG\" && echo 'tuios_check  tuios: Check the Rex Integration'; exit 0 ;;\n" +
			"do) echo '{\"tuios\":\"/opt/test/tuios\",\"version\":\"tuios version test\",\"lua_version\":3,\"agent\":\"claude\",\"agent_installed\":" + found + "}'; exit 0 ;;\n" +
			"esac\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(script), 0o755); err != nil { //nolint:gosec // the fake rex runs
			t.Fatal(err)
		}
		r.env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "REX_CONFIG=" + r.cfg}
		return r
	}
	run := func(t *testing.T, r rig, args ...string) (string, error) {
		t.Helper()
		out, err := tuiosCLIEnv(t, r.base, r.env, append([]string{"integration"}, args...)...)
		saveText(t, strings.Join(args, "-"), out)
		return out, err
	}
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return "<none>"
		}
		return string(b)
	}

	t.Run("fresh", func(t *testing.T) {
		r := newRig(t, false, true)
		out, err := run(t, r, "install", "rex")
		if err != nil {
			t.Fatalf("ASSERTION: install rex failed: %v\n%s", err, out)
		}
		if read(r.lua) != string(shipped) {
			t.Errorf("ASSERTION: the installed tuios.lua is not integrations/rex/tuios.lua")
		}
		if got := read(r.cfg); got != "require(\"tuios\").setup{} -- added by tuios integration\n" {
			t.Errorf("ASSERTION: init.lua is %q, want the one marked line", got)
		}
		if got := read(r.log); got != "config check\nconfig reload\ndo tuios_check\n" {
			t.Errorf("ASSERTION: rex was called as %q, want check, reload, then tuios_check", got)
		}
		if !strings.Contains(out, "The actions run /opt/test/tuios") || !strings.Contains(out, "tuios finds it") {
			t.Errorf("ASSERTION: install does not report what tuios_check found:\n%s", out)
		}
		saveText(t, "init-after", read(r.cfg))
	})

	t.Run("own lines survive and come back", func(t *testing.T) {
		r := newRig(t, false, true)
		own := "-- my Rex config\nrex.bind(\"cmd+k\", \"pane.split\")\nlocal x = 1 -- no newline at the end"
		if err := os.WriteFile(r.cfg, []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, r, "install", "rex"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		if got := read(r.cfg); !strings.HasPrefix(got, own) || strings.Count(got, "require(\"tuios\")") != 1 {
			t.Errorf("ASSERTION: install changed the person's lines or added the require twice:\n%s", got)
		}
		if out, err := run(t, r, "uninstall", "rex"); err != nil {
			t.Fatalf("uninstall: %v\n%s", err, out)
		}
		if got := read(r.cfg); got != own+"\n" {
			t.Errorf("ASSERTION: after uninstall init.lua is %q, want the person's own %q (plus the newline install added)", got, own+"\n")
		}
		if got := read(r.lua); got != "<none>" {
			t.Errorf("ASSERTION: uninstall left tuios.lua behind")
		}
	})

	t.Run("refused config is put back", func(t *testing.T) {
		r := newRig(t, true, true)
		own := "-- mine\n"
		if err := os.WriteFile(r.cfg, []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, r, "install", "rex")
		if err == nil {
			t.Errorf("ASSERTION: install succeeded although rex config check failed:\n%s", out)
		}
		if !strings.Contains(out, "unexpected symbol") || !strings.Contains(out, "put back") {
			t.Errorf("ASSERTION: the failure does not carry Rex's message and say the files were put back:\n%s", out)
		}
		if got := read(r.cfg); got != own {
			t.Errorf("ASSERTION: init.lua is %q after a refused check, want %q", got, own)
		}
		if got := read(r.lua); got != "<none>" {
			t.Errorf("ASSERTION: tuios.lua stayed after a refused check")
		}
	})

	t.Run("hand-written require is left alone", func(t *testing.T) {
		r := newRig(t, false, false)
		own := "require(\"tuios\").setup{ bin = \"/full/path/tuios\" }\n"
		if err := os.WriteFile(r.cfg, []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, r, "install", "rex"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		if got := read(r.cfg); got != own {
			t.Errorf("ASSERTION: install changed a hand-written require: %q", got)
		}
		out, err := run(t, r, "status", "rex")
		if err != nil {
			t.Fatalf("status: %v\n%s", err, out)
		}
		for _, want := range []string{"a line of your own", "tuios.lua: current", "the tuios actions are loaded", "The actions run /opt/test/tuios", "does not find it"} {
			if !strings.Contains(out, want) {
				t.Errorf("ASSERTION: status lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("own tuios.lua needs --force", func(t *testing.T) {
		r := newRig(t, false, true)
		edited := string(shipped) + "\n-- my change\n"
		if err := os.WriteFile(r.lua, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, r, "install", "rex")
		if err == nil || !strings.Contains(out, "--force") {
			t.Errorf("ASSERTION: install replaced an edited tuios.lua without --force:\n%s", out)
		}
		if read(r.lua) != edited {
			t.Errorf("ASSERTION: the edited tuios.lua changed without --force")
		}
		if out, err := run(t, r, "install", "rex", "--force"); err != nil {
			t.Fatalf("install --force: %v\n%s", err, out)
		}
		if read(r.lua) != string(shipped) || read(r.lua+".tuios.bak") != edited {
			t.Errorf("ASSERTION: --force did not replace tuios.lua and keep the edit as tuios.lua.tuios.bak")
		}
	})

	t.Run("older hand copy is updated", func(t *testing.T) {
		r := newRig(t, false, true)
		old := "-- tuios for Rex: actions in Rex's command palette, and optional keys, that\nlocal M = {}\nreturn M\n"
		if err := os.WriteFile(r.lua, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, r, "install", "rex")
		if err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		if read(r.lua) != string(shipped) || !strings.Contains(out, "Replaced tuios.lua version 0") {
			t.Errorf("ASSERTION: an older copy tuios wrote was not replaced, or the output does not say so:\n%s", out)
		}
	})
}

// TestRexActionsComeFromTuios: tuios.lua defines its Rex actions from tuios
// actions --json, not from a list of its own. The real tuios.lua is loaded
// against a stand-in for Rex's Lua API (testdata/rex-load.lua) whose rex.exec
// runs the real binary, with tuios only in ~/.local/bin and a PATH without
// it, as the Rex app gives its server. It needs a Lua (luajit, as Rex runs
// Lua 5.1, or lua) and skips without one. The run in a real Rex server is
// testdata/rex-real.sh.
//
// How it could pass wrongly, written down first:
//   - The Lua could still hold the list: every entry of the binary's own
//     tuios actions --json must come back as one Rex action, in its order,
//     and nothing else but Start an Agent and the check.
//   - The list could come from a tuios on the test machine's PATH: PATH is
//     /usr/bin:/bin, and the first program run must be the one in
//     ~/.local/bin.
//   - The form could offer harnesses of its own: its choices must be the
//     binary's harness commands.
//   - A tuios too old for --json, or none at all, could break Rex's config
//     load: both must load, with the fallback actions.
//   - The list could need the daemon, so Rex start would wait on it or start
//     one: the budget runs it with no daemon, and no socket may appear.
//
// NEGATIVE CONTROL: with M.setup building the actions from the fallback
// whatever tuios answers (M.actions(nil)), the "from the binary" case fails
// on the list of actions. Recorded in NEGATIVE_CONTROLS.md.
func TestRexActionsComeFromTuios(t *testing.T) {
	lua, err := exec.LookPath("luajit")
	if err != nil {
		if lua, err = exec.LookPath("lua"); err != nil {
			t.Skip("no luajit or lua on PATH")
		}
	}
	loader, err := filepath.Abs(filepath.Join("testdata", "rex-load.lua"))
	if err != nil {
		t.Fatal(err)
	}
	luaDir, err := filepath.Abs(filepath.Join("..", "..", "integrations", "rex"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	xdg := func(home string) []string {
		var env []string
		for _, key := range xdgKeys {
			if key != "HOME" {
				env = append(env, key+"="+xdgDir(base, key))
			}
		}
		return append(env, "HOME="+home, "USER="+os.Getenv("USER"), "PATH=/usr/bin:/bin")
	}
	load := func(t *testing.T, home, name string) []string {
		t.Helper()
		cmd := exec.Command(lua, loader, luaDir)
		cmd.Dir = luaDir
		cmd.Env = xdg(home)
		out, err := cmd.CombinedOutput()
		saveText(t, name, string(out))
		if err != nil {
			t.Fatalf("rex-load.lua: %v\n%s", err, out)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	field := func(lines []string, kind string, n int) []string {
		var got []string
		for _, l := range lines {
			parts := strings.Split(l, "\t")
			if parts[0] == kind && len(parts) > n {
				got = append(got, parts[n])
			}
		}
		return got
	}
	newHome := func(t *testing.T, tuios string) string {
		t.Helper()
		home := t.TempDir()
		bin := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if tuios != "" {
			if err := os.Symlink(tuios, filepath.Join(bin, "tuios")); err != nil {
				t.Fatal(err)
			}
		}
		return home
	}
	fallback := []string{"tuios_goto_picker", "tuios_prefix_inbox", "tuios_prefix_mail", "tuios_prefix_next_attention", "tuios_start_agent", "tuios_check"}

	t.Run("from the binary", func(t *testing.T) {
		home := newHome(t, tuiosBin)
		cmd := exec.Command(tuiosBin, "actions", "--json")
		cmd.Env = xdg(home)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("tuios actions --json: %v", err)
		}
		saveText(t, "actions", string(raw))
		var list struct {
			Actions []struct {
				Name, Title, Category string
			}
			Harnesses []struct{ Command string }
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("tuios actions --json is not JSON: %v", err)
		}
		var want, enum []string
		for _, a := range list.Actions {
			if a.Name != "new_agent" {
				want = append(want, "tuios_"+a.Name)
			}
		}
		want = append(want, "tuios_start_agent", "tuios_check")
		for _, h := range list.Harnesses {
			enum = append(enum, h.Command)
		}
		lines := load(t, home, "rex-load")
		if lines[len(lines)-1] != "LOADED" {
			t.Fatalf("ASSERTION: setup did not load:\n%s", strings.Join(lines, "\n"))
		}
		if exe := field(lines, "EXEC", 1); len(exe) == 0 || exe[0] != filepath.Join(home, ".local", "bin", "tuios")+" actions --json" {
			t.Errorf("ASSERTION: the list did not come from the tuios in ~/.local/bin: %q", exe)
		}
		if got := field(lines, "ACTION", 1); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("ASSERTION: Rex's actions are not tuios's list.\n got %d: %v\nwant %d: %v", len(got), got, len(want), want)
		}
		if got := field(lines, "ENUM", 1); strings.Join(got, " ") != strings.Join(enum, " ") {
			t.Errorf("ASSERTION: the start form's harnesses are not tuios's: %v, want %v", got, enum)
		}
		for _, name := range []string{"new_window", "goto_picker", "prefix_inbox", "prefix_mail", "prefix_review", "prefix_selection", "screenshot", "layout_bsp", "new_agent"} {
			found := false
			for _, a := range list.Actions {
				found = found || a.Name == name
			}
			if !found {
				t.Errorf("ASSERTION: tuios actions --json leaves out %s", name)
			}
		}
	})

	t.Run("too old for the list", func(t *testing.T) {
		home := newHome(t, "")
		old := "#!/bin/sh\n[ \"$1 $2\" = 'actions --json' ] && { echo 'Error: unknown flag: --json' >&2; exit 1; }\nexit 0\n"
		if err := os.WriteFile(filepath.Join(home, ".local", "bin", "tuios"), []byte(old), 0o755); err != nil { //nolint:gosec // the stand-in runs
			t.Fatal(err)
		}
		lines := load(t, home, "rex-load-old")
		if lines[len(lines)-1] != "LOADED" {
			t.Fatalf("ASSERTION: an old tuios broke the config load:\n%s", strings.Join(lines, "\n"))
		}
		if got := field(lines, "ACTION", 1); strings.Join(got, " ") != strings.Join(fallback, " ") {
			t.Errorf("ASSERTION: an old tuios did not give the fallback actions: %v", got)
		}
	})

	t.Run("no tuios", func(t *testing.T) {
		// tuios.lua looks in the install folders outside HOME too, so a tuios
		// installed on the test machine is found, as it should be.
		for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/nix/var/nix/profiles/default/bin", "/run/current-system/sw/bin",
			"/etc/profiles/per-user/" + os.Getenv("USER") + "/bin"} {
			if _, err := os.Stat(filepath.Join(d, "tuios")); err == nil {
				t.Skipf("this machine has tuios in %s, which tuios.lua finds", d)
			}
		}
		lines := load(t, newHome(t, ""), "rex-load-none")
		if lines[len(lines)-1] != "LOADED" {
			t.Fatalf("ASSERTION: no tuios broke the config load:\n%s", strings.Join(lines, "\n"))
		}
		if got := field(lines, "ACTION", 1); strings.Join(got, " ") != strings.Join(fallback, " ") {
			t.Errorf("ASSERTION: no tuios did not give the fallback actions: %v", got)
		}
		if got := field(lines, "EXEC", 1); len(got) != 0 {
			t.Errorf("ASSERTION: something ran with no tuios: %v", got)
		}
	})

	// Rex runs tuios actions --json at each config load, so it is time a
	// person waits at Rex start. The fastest of five runs, since load only
	// adds time.
	t.Run("budget", func(t *testing.T) {
		home := newHome(t, tuiosBin)
		best := time.Hour
		for range 5 {
			cmd := exec.Command(tuiosBin, "actions", "--json")
			cmd.Env = xdg(home)
			start := time.Now()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tuios actions --json: %v\n%s", err, out)
			}
			best = min(best, time.Since(start))
		}
		saveText(t, "budget", best.String())
		if best > 50*time.Millisecond {
			t.Errorf("ASSERTION: tuios actions --json took %v at best, over the 50ms budget", best)
		}
		entries, _ := os.ReadDir(xdgDir(base, "XDG_RUNTIME_DIR"))
		for _, e := range entries {
			if strings.Contains(e.Name(), "tuios") {
				t.Errorf("ASSERTION: tuios actions --json made %s, so it reached for a daemon", e.Name())
			}
		}
	})
}
