package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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

	// status says which status records Rex's tabs get, from [agents]
	// host_program_status, so a person can tell whether Rex shows each agent.
	t.Run("status names the records Rex gets", func(t *testing.T) {
		r := newRig(t, false, true)
		if out, err := run(t, r, "install", "rex"); err != nil {
			t.Fatalf("install rex: %v\n%s", err, out)
		}
		out, err := run(t, r, "status", "rex")
		if err != nil || !strings.Contains(out, "Status in Rex: one record for each tuios session.") {
			t.Errorf("ASSERTION: status does not say the session default: %v\n%s", err, out)
		}
		writeConfig(t, r.base, "[agents]\nhost_program_status = \"panes\"\n")
		out, err = run(t, r, "status", "rex")
		if err != nil || !strings.Contains(out, "Status in Rex: one record for each tuios pane") {
			t.Errorf("ASSERTION: status does not say panes: %v\n%s", err, out)
		}
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
	lua := needTool(t, "lua")
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
	load := func(t *testing.T, home, name string, extra ...string) []string {
		t.Helper()
		cmd := exec.Command(lua, loader, luaDir)
		cmd.Dir = luaDir
		cmd.Env = append(xdg(home), extra...)
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

	// The suggested keys are off until asked for, and clear of every key
	// Rex for Mac ships, of the macOS ctrl+cmd shortcuts, and of every key a
	// program in the terminal could get (each holds cmd, which a Mac never
	// sends to a terminal program). How it could pass wrongly: the list of
	// Rex keys could fail to read and match nothing, so it must hold Rex's
	// palette key; a key could be written in another modifier order than
	// Rex's list, so both sides are put in one order before comparing.
	t.Run("suggested keys", func(t *testing.T) {
		home := newHome(t, tuiosBin)
		if got := field(load(t, home, "rex-load-nokeys"), "BIND", 1); len(got) != 0 {
			t.Errorf("ASSERTION: setup{} bound keys: %v", got)
		}
		lines := load(t, home, "rex-load-keys", "REX_LOAD_KEYS=suggested")
		if lines[len(lines)-1] != "LOADED" {
			t.Fatalf("ASSERTION: the suggested keys broke the config load:\n%s", strings.Join(lines, "\n"))
		}
		norm := func(k string) string {
			parts := strings.Split(strings.ToLower(strings.TrimSpace(k)), "+")
			mods := parts[:len(parts)-1]
			slices.Sort(mods)
			return strings.Join(append(mods, parts[len(parts)-1]), "+")
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "rex-mac-keys.txt"))
		if err != nil {
			t.Fatal(err)
		}
		taken := map[string]string{}
		for l := range strings.SplitSeq(string(raw), "\n") {
			if key, action, ok := strings.Cut(l, "\t"); ok && !strings.HasPrefix(l, "#") {
				taken[norm(key)] = action
			}
		}
		if taken[norm("shift+cmd+p")] != "client.palette.toggle" {
			t.Fatalf("ASSERTION: testdata/rex-mac-keys.txt does not hold Rex's palette key, so it was not read")
		}
		for _, k := range []string{"ctrl+cmd+q", "ctrl+cmd+f", "ctrl+cmd+d", "ctrl+cmd+space"} {
			taken[norm(k)] = "macOS"
		}
		binds := map[string]string{}
		for _, l := range lines {
			if p := strings.Split(l, "\t"); p[0] == "BIND" {
				binds[p[2]] = p[1]
			}
		}
		saveText(t, "suggested-keys", fmt.Sprint(binds))
		for _, action := range []string{"tuios_prefix_inbox", "tuios_prefix_next_attention", "tuios_goto_picker", "tuios_prefix_mail", "tuios_start_agent"} {
			if binds[action] == "" {
				t.Errorf("ASSERTION: the suggested keys do not bind %s", action)
			}
		}
		seen := map[string]string{}
		for action, key := range binds {
			n := norm(key)
			if by, ok := taken[n]; ok {
				t.Errorf("ASSERTION: the suggested key %s for %s is %s's", key, action, by)
			}
			if !strings.Contains(n, "cmd+") {
				t.Errorf("ASSERTION: the suggested key %s for %s has no cmd, so a terminal program could need it", key, action)
			}
			if other, ok := seen[n]; ok {
				t.Errorf("ASSERTION: %s binds both %s and %s", key, other, action)
			}
			seen[n] = action
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

// TestRexOffersYourOwnActions: the [[actions]] of the person's config.toml,
// user:NAME, are in tuios actions --json, so tuios.lua defines a Rex action
// for each, tuios_user_NAME, and running it from Rex runs the action in the
// client that shows the focused pane's session.
//
// How it could pass wrongly, written down first:
//   - The action could be listed and never run: it is run through tuios.lua's
//     own run function, against a real daemon and an attached client, and
//     checked by the file it writes.
//   - A name with a dash has no Rex name as it is (Rex takes lowercase
//     letters, digits and underscores): user:mark-it must be
//     tuios_user_mark_it, and user:mark_it, which maps to the same name, is
//     left out, not defined twice.
//   - A label is the person's text, and could carry an escape sequence into
//     Rex's palette: the listed title holds no control byte.
//   - An action offered only on a card has nothing to run on from Rex: it is
//     not listed.
//   - A config.toml tuios cannot parse could cost Rex every tuios action, or
//     the person's whole init.lua: the built-in actions are still listed,
//     setup still loads, and config_note says why.
//   - Listing could write a config.toml into a folder that has none: after
//     the run with no config, the config folder holds no file.
//
// Artifact: tuios actions --json, the rex-load transcripts, and the file the
// action wrote.
//
// NEGATIVE CONTROL: with userOutsideActions returning nothing, the list
// check fails on user:mark-it. With M.rex_name mapping a user action to
// "tuios_" .. name, Rex has no tuios_user_mark_it and the run fails.
func TestRexOffersYourOwnActions(t *testing.T) {
	lua := needTool(t, "lua")
	loader, err := filepath.Abs(filepath.Join("testdata", "rex-load.lua"))
	if err != nil {
		t.Fatal(err)
	}
	luaDir, err := filepath.Abs(filepath.Join("..", "..", "integrations", "rex"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	killDaemon(t, base)
	art := artifactDir(t)
	out := filepath.Join(base, "marked.out")
	writeConfig(t, base, `[[actions]]
name = "mark-it"
label = "Mark it `+"\\u001b[31m"+`now"
run = ["/bin/sh", "-c", "echo marked > `+out+`"]
in = "silent"

[[actions]]
name = "mark_it"
label = "Same Rex name"
action = "new_window"

[[actions]]
name = "card-only"
targets = ["card"]
action = "new_window"
`)
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tuiosBin, filepath.Join(bin, "tuios")); err != nil {
		t.Fatal(err)
	}
	env := func(b string, extra ...string) []string {
		var e []string
		for _, key := range xdgKeys {
			if key != "HOME" {
				e = append(e, key+"="+xdgDir(b, key))
			}
		}
		return append(append(e, "HOME="+home, "USER="+os.Getenv("USER"), "PATH=/usr/bin:/bin"), extra...)
	}
	load := func(t *testing.T, b, name string, extra ...string) []string {
		t.Helper()
		cmd := exec.Command(lua, loader, luaDir)
		cmd.Dir = luaDir
		cmd.Env = env(b, extra...)
		o, err := cmd.CombinedOutput()
		saveText(t, name, string(o))
		if err != nil {
			t.Fatalf("rex-load.lua: %v\n%s", err, o)
		}
		return strings.Split(strings.TrimSpace(string(o)), "\n")
	}
	type listed struct {
		Actions []struct {
			Name, Title, Category string
			User                  bool
		}
		ConfigNote string `json:"config_note"`
	}
	list := func(t *testing.T, b, name string) listed {
		t.Helper()
		cmd := exec.Command(tuiosBin, "actions", "--json")
		cmd.Env = env(b)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("tuios actions --json: %v", err)
		}
		saveText(t, name, string(raw))
		var l listed
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("tuios actions --json is not JSON: %v", err)
		}
		return l
	}

	l := list(t, base, "actions")
	var own []string
	for _, a := range l.Actions {
		if a.User {
			own = append(own, a.Name)
			if strings.ContainsAny(a.Title, "\x1b\x07\n") {
				t.Errorf("ASSERTION: the title of %s holds a control byte: %q", a.Name, a.Title)
			}
		}
	}
	if strings.Join(own, " ") != "user:mark-it user:mark_it" {
		t.Errorf("ASSERTION: the listed user actions are %v, want user:mark-it and user:mark_it, and no card-only", own)
	}

	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	attachIn(t, base, "S", startOpts{cols: 120, rows: 36})
	var pid int
	waitUntil(t, "a client of S in tuios list-clients", 15*time.Second, func() (bool, string) {
		o, err := tuiosCLI(t, base, "list-clients", "--json")
		if err != nil {
			return false, err.Error()
		}
		var cs []struct {
			Pid     int    `json:"pid"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal([]byte(o), &cs); err != nil {
			return false, o
		}
		for _, c := range cs {
			if c.Session == "S" && c.Pid > 0 {
				pid = c.Pid
			}
		}
		return pid != 0, o
	})
	lines := load(t, base, "rex-run", "REX_LOAD_RUN=tuios_user_mark_it", "REX_LOAD_PID="+strconv.Itoa(pid))
	var names []string
	for _, ln := range lines {
		if p := strings.Split(ln, "\t"); p[0] == "ACTION" && strings.HasPrefix(p[1], "tuios_user_") {
			names = append(names, p[1]+" "+p[2])
		}
	}
	if strings.Join(names, ", ") != "tuios_user_mark_it tuios: Mark it [31mnow" {
		t.Errorf("ASSERTION: Rex's user actions are %q, want only tuios_user_mark_it with the label as written", names)
	}
	if lines[len(lines)-1] != "RAN\ttuios_user_mark_it" {
		t.Fatalf("ASSERTION: the Rex action did not run:\n%s", strings.Join(lines, "\n"))
	}
	got := waitForFileText(t, out, func(s string) bool { return strings.TrimSpace(s) == "marked" }, "the user action to write its file")
	_ = os.WriteFile(filepath.Join(art, "marked.out"), []byte(got), 0o644)

	t.Run("config tuios cannot read", func(t *testing.T) {
		b := t.TempDir()
		dir := filepath.Join(b, "XDG_CONFIG_HOME", "tuios")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[[actions]\nname="), 0o600); err != nil {
			t.Fatal(err)
		}
		l := list(t, b, "actions-bad")
		if l.ConfigNote == "" || len(l.Actions) < 10 {
			t.Errorf("ASSERTION: a broken config.toml gave %d actions and note %q", len(l.Actions), l.ConfigNote)
		}
		lines := load(t, b, "rex-load-bad")
		if lines[len(lines)-1] != "LOADED" {
			t.Fatalf("ASSERTION: a broken config.toml broke the Rex config load:\n%s", strings.Join(lines, "\n"))
		}
	})

	t.Run("no config writes none", func(t *testing.T) {
		b := t.TempDir()
		list(t, b, "actions-none")
		entries, _ := os.ReadDir(filepath.Join(b, "XDG_CONFIG_HOME"))
		for _, e := range entries {
			t.Errorf("ASSERTION: tuios actions --json made %s in an empty config folder", e.Name())
		}
	})
}
