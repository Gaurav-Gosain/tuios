package tuie2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tuios draws eight Mermaid types itself. Another type needs the sketch
// plugin: tuios mermaid says what to run, and with the plugin it runs the
// plugin's program. A TML panel shows the source and the person hears once
// per type what to do.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "The sketch plugin".

const rareDiagram = "timeline\n    title History\n    2002 : LinkedIn\n"

func TestSketchPlugin(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	cwd := workDirIn(t, base)
	if err := os.WriteFile(filepath.Join(cwd, "t.mmd"), []byte(rareDiagram), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "f.mmd"), []byte("flowchart LR\n  A --> B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A core type still draws.
	if out, err := tuiosCLI(t, base, "mermaid", "f.mmd", "-f", "svg"); err != nil || !strings.Contains(out, "<svg") {
		t.Fatalf("a flowchart: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "mermaid", "t.mmd", "-f", "text")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(out, "this diagram needs the sketch plugin. Run tuios plugins install sketch") {
		t.Fatalf("a timeline with no plugin: %v, want exit 3 and the install command\n%s", err, out)
	}

	// A panel with the timeline, twice, and a flowchart beside it.
	if out, err := tuiosCLI(t, base, "new", "sk", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "sk"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	doc := `<tml version="1" title="Diagrams"><col><mermaid>` + rareDiagram + `</mermaid></col></tml>`
	if err := os.WriteFile(filepath.Join(cwd, "d.tml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "tml", "show", "d.tml", "-s", "sk"); err != nil {
		t.Fatalf("tml show: %v\n%s", err, out)
	}
	if err := term.WaitForText("Run tuios plugins install sketch to draw this diagram.", uiTimeout); err != nil {
		t.Fatalf("no notice for the timeline: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("2002 : LinkedIn", uiTimeout); err != nil {
		t.Fatalf("the panel does not show the source: %v\n%s", err, term.Snapshot())
	}
	art := artifactDir(t)
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "notice-and-source")

	// The notice goes, and a second panel of the same type brings no second
	// notice.
	deadline := time.Now().Add(15 * time.Second)
	for strings.Contains(term.Snapshot(), "Run tuios plugins install sketch to draw this diagram.") {
		if time.Now().After(deadline) {
			t.Fatalf("the notice stays:\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "tml", "show", "d.tml", "-s", "sk"); err != nil {
		t.Fatalf("tml show again: %v\n%s", err, out)
	}
	time.Sleep(2 * time.Second)
	if strings.Contains(term.Snapshot(), "Run tuios plugins install sketch to draw this diagram.") {
		t.Errorf("a second panel of the same type told the person again:\n%s", term.Snapshot())
	}

	// With a plugin: its program draws the timeline, with the flags.
	plug := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plug, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sketch", "tuios-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, plug, map[string]string{"tuios-plugin.toml": string(manifest)})
	if err := os.WriteFile(filepath.Join(plug, "bin", "sketch"), []byte("#!/bin/sh\necho \"plugin drew: $*\"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", plug, "--yes"); err != nil {
		t.Fatalf("install the plugin: %v\n%s", err, out)
	}
	out, err = tuiosCLI(t, base, "mermaid", "t.mmd", "-f", "text", "--cols", "60")
	if err != nil || !strings.Contains(out, "plugin drew: -f text -cols 60 -") || !strings.Contains(out, "2002 : LinkedIn") {
		t.Fatalf("a timeline with the plugin: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "mermaid", "t.mmd", "--fit"); err == nil || !strings.Contains(out, "does not take") {
		t.Errorf("a flag the plugin does not take: %v\n%s", err, out)
	}
}
