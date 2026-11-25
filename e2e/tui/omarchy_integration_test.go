package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// omarchyRig is a home folder with an Omarchy config folder, and stand-ins
// for the Omarchy programs on PATH. Each stand-in writes how it was called
// to a log. omarchy-menu-select prints the first row that matches
// OMARCHY_PICK, as the menu prints the row a person chose.
type omarchyRig struct {
	base, home, log string
	env             []string
}

func newOmarchyRig(t *testing.T) omarchyRig {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	bin := filepath.Join(base, "bin")
	for _, d := range []string{bin, filepath.Join(home, ".config", "omarchy", "extensions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := omarchyRig{base: base, home: home, log: filepath.Join(base, "omarchy.log")}
	logLine := "printf '%s\\n' \"$(basename \"$0\") $*\" >> '" + r.log + "'\n"
	stubs := map[string]string{
		"omarchy-shell":      logLine + "echo ok\n",
		"omarchy":            logLine + "echo ok\n",
		"omarchy-launch-tui": logLine,
		"hyprctl":            "exit 1\n",
		"omarchy-menu-select": "printf 'select %s\\n' \"$1\" >> '" + r.log + "'\nshift\n" +
			"for row in \"$@\"; do printf 'row %s\\n' \"$row\" >> '" + r.log + "'; done\n" +
			"for row in \"$@\"; do case \"$row\" in *$OMARCHY_PICK*) printf '%s\\n' \"${row#*\"$(printf '\\t')\"}\"; exit 0 ;; esac; done\nexit 1\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // the stand-ins run
			t.Fatal(err)
		}
	}
	r.env = []string{"HOME=" + home, "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	return r
}

// menu runs the tuios-menu script install wrote, with tuios's own
// environment for the test's daemon.
func (r omarchyRig) menu(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(r.home, ".config", "omarchy", "plugins", "tuios.agents", "tuios-menu"), args...)
	cmd.Env = append(cliEnv(r.base), r.env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tuios-menu %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r omarchyRig) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := tuiosCLIEnv(t, r.base, r.env, args...)
	saveText(t, strings.Join(args, "-"), out)
	if err != nil {
		t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// waitLog waits until the log holds want, since the menu does not wait for
// the terminal it opens, then takes the log.
func (r omarchyRig) waitLog(t *testing.T, want string) string {
	t.Helper()
	waitTextOf(t, "a log line "+want, uiTimeout*3, func() string {
		b, _ := os.ReadFile(r.log)
		return string(b)
	}, func(s string) bool { return strings.Contains(s, want) })
	return r.takeLog(t)
}

func (r omarchyRig) takeLog(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(r.log)
	_ = os.Remove(r.log)
	return string(b)
}

// omarchyJSONC reads a menu file the way the Omarchy menu does: whole-line
// comments removed, then trailing commas.
func omarchyJSONC(t *testing.T, data string) map[string]map[string]string {
	t.Helper()
	var kept []string
	for _, l := range strings.Split(data, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			kept = append(kept, l)
		}
	}
	text := regexp.MustCompile(`,(\s*})`).ReplaceAllString(strings.Join(kept, "\n"), "$1")
	var out map[string]map[string]string
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("ASSERTION: the Omarchy menu could not read the file: %v\n%s", err, data)
	}
	return out
}

// TestOmarchyIntegration: integration install omarchy puts the tuios agents
// widget in Omarchy's plugin folder and a tuios row in the Omarchy menu, and
// has the shell load and turn on the widget. The tuios-menu script it writes
// lists what needs the person and the sessions, and opens the choice in a
// terminal.
// Stand-ins take the place of the Omarchy programs, under a home folder of
// the test's own.
//
// How it could pass wrongly, written down first:
//   - Install could change the person's own menu rows, or write a file the
//     Omarchy menu cannot read: the file is read back the way Omarchy reads
//     it, and uninstall must give back the old bytes.
//   - The widget could be a copy the source tests never saw: the files are
//     compared with integrations/omarchy, with the tuios path filled in.
//   - The shell could be left with the old plugin list: the log must hold
//     rescanPlugins, then plugin enable.
//   - A plugin folder of the person's own could be overwritten: install
//     must refuse it without --force.
//   - The menu could show rows without acting: choosing the approval must
//     open a terminal that attaches its session, and choosing a session must
//     attach that session.
//
// NEGATIVE CONTROL: with the menu row write cut from OmarchyInstall, the
// read-back of the menu finds no tuios row and the install subtest fails.
func TestOmarchyIntegration(t *testing.T) {
	src := filepath.Join("..", "..", "integrations", "omarchy", "tuios.agents")
	wantWidget, err := os.ReadFile(filepath.Join(src, "Widget.qml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(src, "tuios-menu"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the tuios menu needs jq, as Omarchy has it")
	}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pluginOf := func(home string) string { return filepath.Join(home, ".config", "omarchy", "plugins", "tuios.agents") }
	wantManifest := func(home string) string {
		m := strings.Replace(string(manifest), `"command": "tuios"`, `"command": `+quote(tuiosBin), 1)
		return strings.Replace(m, `"menu": "tuios-menu"`, `"menu": `+quote(filepath.Join(pluginOf(home), "tuios-menu")), 1)
	}
	wantScript := strings.Replace(string(script), "\ntuios=tuios\n", "\ntuios="+tuiosBin+"\n", 1)

	t.Run("install, again and uninstall", func(t *testing.T) {
		r := newOmarchyRig(t)
		menu := filepath.Join(r.home, ".config", "omarchy", "extensions", "omarchy-menu.jsonc")
		own := "{\n  // my rows\n  \"personal\": {\"icon\":\"x\",\"label\":\"Personal\"},\n  \"personal.notes\": {\"label\":\"Notes\",\"action\":\"notes\"}\n}\n"
		if err := os.WriteFile(menu, []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		out := r.run(t, "integration", "install", "omarchy")
		plugin := filepath.Join(r.home, ".config", "omarchy", "plugins", "tuios.agents")
		if b, _ := os.ReadFile(filepath.Join(plugin, "Widget.qml")); string(b) != string(wantWidget) {
			t.Errorf("ASSERTION: the installed Widget.qml is not integrations/omarchy/tuios.agents/Widget.qml")
		}
		if b, _ := os.ReadFile(filepath.Join(plugin, "manifest.json")); string(b) != wantManifest(r.home) {
			t.Errorf("ASSERTION: the installed manifest.json is not the source with the tuios and menu paths:\n%s", b)
		}
		if b, _ := os.ReadFile(filepath.Join(plugin, "tuios-menu")); string(b) != wantScript {
			t.Errorf("ASSERTION: the installed tuios-menu is not the source with the tuios path:\n%s", b)
		}
		if fi, err := os.Stat(filepath.Join(plugin, "tuios-menu")); err != nil || fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("ASSERTION: the installed tuios-menu does not run: %v", err)
		}
		got, _ := os.ReadFile(menu)
		saveText(t, "menu-after-install", string(got))
		rows := omarchyJSONC(t, string(got))
		if rows["tuios"]["action"] != filepath.Join(plugin, "tuios-menu") || rows["tuios"]["label"] != "tuios" {
			t.Errorf("ASSERTION: the menu has no tuios row that runs this tuios: %+v", rows["tuios"])
		}
		if rows["personal.notes"]["action"] != "notes" || rows["personal"]["label"] != "Personal" {
			t.Errorf("ASSERTION: the person's own rows did not survive: %+v", rows)
		}
		if log := r.takeLog(t); log != "omarchy-shell shell rescanPlugins\nomarchy plugin enable tuios.agents --yes\n" {
			t.Errorf("ASSERTION: the Omarchy programs were called as %q, want rescanPlugins then plugin enable", log)
		}
		if !strings.Contains(out, "Turned on the tuios agents widget") {
			t.Errorf("ASSERTION: install does not say the widget is on:\n%s", out)
		}

		r.run(t, "integration", "install", "omarchy")
		if again, _ := os.ReadFile(menu); string(again) != string(got) {
			t.Errorf("ASSERTION: a second install changed the menu file:\n%s", again)
		}
		r.takeLog(t)
		status := r.run(t, "integration", "status", "omarchy")
		if !strings.Contains(status, "Bar widget: current") || !strings.Contains(status, "Menu: the tuios row is in") {
			t.Errorf("ASSERTION: status does not report the widget and the row:\n%s", status)
		}

		r.run(t, "integration", "uninstall", "omarchy")
		if back, _ := os.ReadFile(menu); string(back) != own {
			t.Errorf("ASSERTION: uninstall did not give the menu file back as it was:\n%s", back)
		}
		if _, err := os.Stat(plugin); !os.IsNotExist(err) {
			t.Errorf("ASSERTION: uninstall left %s", plugin)
		}
		if log := r.takeLog(t); !strings.HasPrefix(log, "omarchy plugin disable tuios.agents --yes\n") {
			t.Errorf("ASSERTION: uninstall did not turn the widget off first: %q", log)
		}
	})

	t.Run("a plugin of the person's own stays", func(t *testing.T) {
		r := newOmarchyRig(t)
		plugin := filepath.Join(r.home, ".config", "omarchy", "plugins", "tuios.agents")
		if err := os.MkdirAll(plugin, 0o755); err != nil {
			t.Fatal(err)
		}
		mine := "import QtQuick\nItem {}\n"
		if err := os.WriteFile(filepath.Join(plugin, "Widget.qml"), []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := tuiosCLIEnv(t, r.base, r.env, "integration", "install", "omarchy")
		if err == nil || !strings.Contains(out, "--force") {
			t.Errorf("ASSERTION: install did not refuse a plugin folder it did not write: %v\n%s", err, out)
		}
		if b, _ := os.ReadFile(filepath.Join(plugin, "Widget.qml")); string(b) != mine {
			t.Errorf("ASSERTION: install changed the person's Widget.qml")
		}
	})

	t.Run("the menu goes to what needs you", func(t *testing.T) {
		r := newOmarchyRig(t)
		r.run(t, "new", "work", "--detach")
		r.run(t, "new", "docs", "--detach")
		r.run(t, "set-agent-state", "-s", "work", "needs_input", "--kind", "approval", "-m", "Allow Bash: make deploy")
		waitTextOf(t, "the approval in bar-status", uiTimeout*3, func() string {
			out, _ := tuiosCLIEnv(t, r.base, r.env, "bar-status")
			return out
		}, func(s string) bool { return strings.Contains(s, `"needs-you"`) })

		r.run(t, "integration", "install", "omarchy")
		r.takeLog(t)
		r.env = append(r.env, "OMARCHY_PICK=Approval")
		r.menu(t)
		log := r.waitLog(t, "omarchy-launch-tui ")
		saveText(t, "menu-root", log)
		for _, want := range []string{
			"select tuios",
			"Approval · work · Allow Bash: make deploy",
			"\tOpen the inbox\t1 item needs you",
			"\tSessions\tThis machine",
			"omarchy-launch-tui --app-id=org.omarchy.tuios " + tuiosBin + " attach work",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("ASSERTION: the menu log has no %q:\n%s", want, log)
			}
		}

		r.env[len(r.env)-1] = "OMARCHY_PICK=docs"
		r.menu(t, "sessions")
		log = r.waitLog(t, "omarchy-launch-tui ")
		saveText(t, "menu-sessions", log)
		for _, want := range []string{"select Sessions", "\tdocs\t1 pane", "\twork\t1 pane", "\tNew session",
			"omarchy-launch-tui --app-id=org.omarchy.tuios " + tuiosBin + " attach docs"} {
			if !strings.Contains(log, want) {
				t.Errorf("ASSERTION: the sessions menu log has no %q:\n%s", want, log)
			}
		}

		r.run(t, "kill-server")
		r.env[len(r.env)-1] = "OMARCHY_PICK=nothing-matches"
		r.menu(t)
		if log := r.takeLog(t); !strings.Contains(log, "\tStart tuios\ttuios is not running") || strings.Contains(log, "omarchy-launch-tui") {
			t.Errorf("ASSERTION: with no daemon the menu does not offer Start tuios only, or ran a row nobody chose:\n%s", log)
		}
	})
}
