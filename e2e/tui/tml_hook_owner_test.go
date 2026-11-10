package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The owners of the TML surfaces that callers outside every pane open. The
// daemon's hooks together are one owner, each herdr plugin is one, and the
// other processes tuios starts outside every pane, such as the line of an
// exec, are a third. Each changes and closes only what its own kind opened;
// the person changes and closes everything, and reads stay as they are.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "The owners of hook and
// herdr plugin surfaces".

const hookOwnerManifest = `id = "e2e.hookowner"
name = "Owner probe"
version = "1.0.0"
min_herdr_version = "0.9.5"

[[actions]]
id = "step"
title = "Step"
contexts = ["pane"]
command = ["sh", "step.sh"]
`

// ownerStepPrelude starts every step script: run keeps the exit code and the
// output of one call under its row name, open opens a form as a pane and
// keeps its id, and sid reads an id back.
const ownerStepPrelude = `run() { n=$1; shift; "$@" >"$D/$n.out" 2>&1; echo $? >"$D/$n.code"; }
open() { n=$1; shift; "$T" tml show "$D/form.tml" --pane "$@" >"$D/$n.open" 2>&1; awk '/^tml-/ {print $1; exit}' "$D/$n.open" >"$D/$n.id"; }
sid() { cat "$D/$1.id"; }
`

// writeOwnerStep writes the step script at path. It ends by writing
// name.done, which the test waits for.
func writeOwnerStep(t *testing.T, path, rows, name, body string) {
	t.Helper()
	script := "D=" + rows + "\nT=" + tuiosBin + "\n" + ownerStepPrelude + body + "echo ok >\"$D/" + name + ".done\"\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readOwnerRow is the exit code and the output a step kept for a row.
func readOwnerRow(rows, name string) (string, string) {
	code, _ := os.ReadFile(filepath.Join(rows, name+".code"))
	out, _ := os.ReadFile(filepath.Join(rows, name+".out"))
	return strings.TrimSpace(string(code)), string(out)
}

func TestTMLOwnersKeepHooksAndHerdrPluginsApart(t *testing.T) {
	const id = "e2e.hookowner"
	base := t.TempDir()
	rows := filepath.Join(base, "rows")
	if err := os.MkdirAll(rows, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, rows, map[string]string{
		"form.tml":    `<tml version="1" title="Owner form"><text>a form outside every pane</text></tml>` + "\n",
		"replace.tml": `<tml version="1" title="Owner form"><text>replaced</text></tml>` + "\n",
	})
	patch := `'[{"op":"add","path":"/by","value":"someone"}]'`

	// The hook runs a step once each time it is armed. Every window opens
	// a hook run, the hook's own forms too, so the run that takes the
	// armed file is the one that runs the step.
	armed := filepath.Join(base, "armed")
	hookStep := filepath.Join(base, "hook-step.sh")
	hook := filepath.Join(base, "hook.sh")
	body := "#!/bin/sh\nmv " + armed + " " + armed + ".$$ 2>/dev/null || exit 0\n. " + hookStep + "\n"
	if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[hooks]\nafter-new-window = \""+hook+"\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	hookRun := func(n int, step string) {
		t.Helper()
		name := "hook" + strconv.Itoa(n)
		writeOwnerStep(t, hookStep, rows, name, step)
		if err := os.WriteFile(armed, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := tuiosCLI(t, base, "new-window", "-s", "work", "trigger"+strconv.Itoa(n), "--no-focus"); err != nil {
			t.Fatalf("open the trigger window: %v\n%s", err, out)
		}
		waitFile(t, filepath.Join(rows, name+".done"), func(s string) bool { return strings.Contains(s, "ok") }, "hook run "+name)
	}

	// A herdr plugin, installed as the person installs one, runs a step as
	// its action.
	plugin := t.TempDir()
	herdrStep := filepath.Join(base, "herdr-step.sh")
	writeFiles(t, plugin, map[string]string{"herdr-plugin.toml": hookOwnerManifest, "step.sh": ". " + herdrStep + "\n"})
	if out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes"); err != nil {
		t.Fatalf("install the herdr plugin: %v\n%s", err, out)
	}
	herdrRun := func(name, step string) {
		t.Helper()
		writeOwnerStep(t, herdrStep, rows, name, step)
		if out, err := tuiosCLI(t, base, "plugins", "run", id, "step", "--wait"); err != nil {
			t.Fatalf("plugins run %s: %v\n%s", name, err, out)
		}
		waitFile(t, filepath.Join(rows, name+".done"), func(s string) bool { return strings.Contains(s, "ok") }, "herdr run "+name)
	}

	// The person's exec runs a line as a daemon child outside every pane.
	execStep := filepath.Join(base, "exec-step.sh")
	execRun := func(name, step string) {
		t.Helper()
		writeOwnerStep(t, execStep, rows, name, step)
		if out, err := tuiosCLI(t, base, "call", "exec", `{"line":"sh `+execStep+`","timeout_ms":60000}`); err != nil {
			t.Fatalf("exec %s: %v\n%s", name, err, out)
		}
		waitFile(t, filepath.Join(rows, name+".done"), func(s string) bool { return strings.Contains(s, "ok") }, "exec run "+name)
	}
	formID := func(name string) string {
		t.Helper()
		b, _ := os.ReadFile(filepath.Join(rows, name+".id"))
		s := strings.TrimSpace(string(b))
		if !strings.HasPrefix(s, "tml-") {
			o, _ := os.ReadFile(filepath.Join(rows, name+".open"))
			t.Fatalf("form %s did not open:\n%s", name, o)
		}
		return s
	}

	hookRun(1, "open h1 -s \"$TUIOS_SESSION_ID\"\nopen h2 -s \"$TUIOS_SESSION_ID\"\n")
	herdrRun("herdr1", "open r1 -s work\nopen r2 -s work\n")
	for _, f := range []string{"h1", "h2", "r1", "r2"} {
		formID(f)
	}
	execRun("exec1", "open e1 -s work\n"+
		"run e1patch \"$T\" tml patch \"$(sid e1)\" "+patch+"\n"+
		"run c \"$T\" tml patch \"$(sid h1)\" "+patch+"\n"+
		"run d \"$T\" tml patch \"$(sid r1)\" "+patch+"\n"+
		"run execget \"$T\" tml get \"$(sid h1)\"\n")
	formID("e1")
	hookRun(2, "run b \"$T\" tml patch \"$(sid r1)\" "+patch+"\n"+
		"run h2patch \"$T\" tml patch \"$(sid h2)\" "+patch+"\n"+
		"run h2close \"$T\" tml close \"$(sid h2)\"\n"+
		"run hookget \"$T\" tml get \"$(sid r1)\"\n")
	herdrRun("herdr2", "run herdrget \"$T\" tml get \"$(sid h1)\"\n"+
		"run r2patch \"$T\" tml patch \"$(sid r2)\" "+patch+"\n"+
		"run r2close \"$T\" tml close \"$(sid r2)\"\n"+
		"run apatch \"$T\" tml patch \"$(sid h1)\" "+patch+"\n"+
		"run areplace \"$T\" tml replace \"$(sid h1)\" \"$D/replace.tml\"\n"+
		"run aclose \"$T\" tml close \"$(sid h1)\"\n")

	// Refused: each owner's forms are its own.
	for _, r := range []struct{ name, what string }{
		{"apatch", "the herdr plugin's patch of the hook's form"},
		{"areplace", "the herdr plugin's replace of the hook's form"},
		{"aclose", "the herdr plugin's close of the hook's form"},
		{"b", "a second hook run's patch of the herdr plugin's form"},
		{"c", "the exec line's patch of the hook's form"},
		{"d", "the exec line's patch of the herdr plugin's form"},
	} {
		if code, out := readOwnerRow(rows, r.name); code != "1" || !strings.Contains(out, "only for the pane that opened it") {
			t.Errorf("%s: exit %s, want 1 and the owner's refusal\n%s", r.what, code, out)
		}
	}
	// Served: an owner changes its own forms, one hook run what another
	// opened, and every class still reads every form.
	for _, r := range []struct{ name, what string }{
		{"e1patch", "the exec line's patch of its own form"},
		{"h2patch", "a second hook run's patch of the first run's form"},
		{"h2close", "a second hook run's close of the first run's form"},
		{"r2patch", "the herdr plugin's patch of its own form"},
		{"r2close", "the herdr plugin's close of its own form"},
		{"execget", "the exec line's get of the hook's form"},
		{"hookget", "the hook's get of the herdr plugin's form"},
		{"herdrget", "the herdr plugin's get of the hook's form"},
	} {
		if code, out := readOwnerRow(rows, r.name); code != "0" {
			t.Errorf("%s: exit %s, want 0\n%s", r.what, code, out)
		}
	}

	// The person changes and closes both.
	for _, f := range []string{"h1", "r1"} {
		s := formID(f)
		if out, err := tuiosCLI(t, base, "tml", "patch", s, `[{"op":"add","path":"/by","value":"the person"}]`); err != nil {
			t.Errorf("the person's patch of %s: %v\n%s", f, err, out)
		}
		if out, err := tuiosCLI(t, base, "tml", "close", s); err != nil {
			t.Errorf("the person's close of %s: %v\n%s", f, err, out)
		}
	}
}
