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

// TestDoctorShellFlagsPromptMarksOnly checks the pane that marks its prompts
// and not its commands, which bash before 4.4 does with the bash recipe. It
// looks marked, but run refuses it, so the report says so, counts it as
// missing and prints the recipe with its bash version note.
func TestDoctorShellFlagsPromptMarksOnly(t *testing.T) {
	no := false
	panes := func(string) ([]shellPane, bool) {
		return []shellPane{
			{Session: "work", Window: "11111111-aaaa", Name: "old", Marks: true, CommandMarkSeen: &no, PromptOnly: true},
			{Session: "work", Window: "22222222-bbbb", Name: "fresh", Marks: true, AtPrompt: true, CommandMarkSeen: &no},
		}, true
	}
	r := doctorShell("", panes, "/bin/bash")
	if !strings.Contains(r.Setup, "4.4") {
		t.Fatalf("report = %+v, want the bash recipe with its version note", r)
	}
	var out bytes.Buffer
	if err := printDoctorShell(&out, r, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "(old) prompt marks only") {
		t.Fatalf("report does not name the prompt-only pane:\n%s", text)
	}
	if !strings.Contains(text, "(fresh) marks its prompts; no command has run yet") {
		t.Fatalf("report calls a pane with no command marked yet one that marks commands:\n%s", text)
	}
}
