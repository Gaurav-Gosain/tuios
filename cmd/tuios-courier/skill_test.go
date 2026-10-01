package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/skills"
)

// Every tuios-courier command the skill shows must exist in this build, with
// the flags it uses.
func TestSkillCommandsResolve(t *testing.T) {
	root := newRootCmd()
	n := 0
	for line := range strings.SplitSeq(skills.Courier, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "tuios-courier ")
		if !ok {
			continue
		}
		var words []string
		for _, w := range strings.Fields(rest) {
			if strings.HasPrefix(w, "'") || strings.HasPrefix(w, "\"") || strings.HasPrefix(w, "#") || w == "|" || w == "&&" {
				break
			}
			words = append(words, w)
		}
		if len(words) == 0 || words[0] == "--skill" {
			continue
		}
		cmd, args, err := root.Find(words)
		if err != nil || cmd == root {
			t.Errorf("skill line %q: no such command (%v)", line, err)
			continue
		}
		for _, a := range args {
			name, isFlag := strings.CutPrefix(a, "--")
			if !isFlag {
				continue
			}
			name, _, _ = strings.Cut(name, "=")
			if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				t.Errorf("skill line %q: %s has no flag --%s", line, cmd.CommandPath(), name)
			}
		}
		n++
	}
	if n < 8 {
		t.Fatalf("only %d tuios-courier commands found in the skill", n)
	}
}

func TestSkillFlagPrintsIt(t *testing.T) {
	out, err := exec.Command(courierBin(t), "--skill").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != skills.Courier {
		t.Fatal("tuios-courier --skill does not print the embedded skill")
	}
}

// The courier is a separate binary so that it adds no way into the daemon.
// It must not link the daemon, the link layer, the window manager or the SSH
// server.
func TestIsolationFromTheDaemon(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"github.com/Gaurav-Gosain/tuios/internal/session",
		"github.com/Gaurav-Gosain/tuios/internal/federation",
		"github.com/Gaurav-Gosain/tuios/internal/app",
		"github.com/Gaurav-Gosain/tuios/internal/server",
		"github.com/Gaurav-Gosain/tuios/internal/terminal",
	} {
		for dep := range strings.SplitSeq(string(out), "\n") {
			if dep == forbidden {
				t.Errorf("tuios-courier links %s", forbidden)
			}
		}
	}
}
