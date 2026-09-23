package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestDoctorShellNamesThePanesWithoutMarks checks what the report is for: a
// pane whose shell sends no OSC 133 marks is named, and the lines that turn
// them on for the person's own shell are printed. With every pane marking,
// no recipe is printed.
func TestDoctorShellNamesThePanesWithoutMarks(t *testing.T) {
	panes := func(string) ([]shellPane, bool) {
		return []shellPane{
			{Session: "work", Window: "11111111-aaaa", Name: "build", Marks: true, AtPrompt: true, Commands: 3},
			{Session: "work", Window: "22222222-bbbb", Name: "logs"},
		}, true
	}
	r := doctorShell("", panes, "/bin/zsh")
	if r.Shell != "zsh" || !strings.Contains(r.Setup, "add-zsh-hook") {
		t.Fatalf("report = %+v, want the zsh recipe", r)
	}
	var out bytes.Buffer
	if err := printDoctorShell(&out, r, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "(logs) no marks") || !strings.Contains(text, "(build) marks its commands") {
		t.Fatalf("report does not say which pane lacks marks:\n%s", text)
	}
	if !strings.Contains(text, "133;C") {
		t.Fatalf("report does not print the recipe:\n%s", text)
	}

	all := func(string) ([]shellPane, bool) {
		return []shellPane{{Session: "work", Window: "1", Name: "build", Marks: true, AtPrompt: true}}, true
	}
	if r := doctorShell("", all, "/bin/bash"); r.Setup != "" {
		t.Fatalf("every pane marks, yet a recipe was chosen: %q", r.Setup)
	}
}
