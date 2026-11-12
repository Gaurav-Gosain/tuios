package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			"actions) grep -q 'require(\"tuios\")' \"$REX_CONFIG\" && echo 'tuios_agents  tuios: Open the Agents View'; exit 0 ;;\n" +
			"do) echo '{\"tuios\":\"/opt/test/tuios\",\"version\":\"tuios version test\",\"lua_version\":2,\"agent\":\"claude\",\"agent_installed\":" + found + "}'; exit 0 ;;\n" +
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
