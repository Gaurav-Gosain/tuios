package main

import (
	"strings"
	"testing"
)

// TestRemovedWorktreeSentencesSayWhereTheWorkWent pins the three outcomes of a
// removal as the person reads them: stashed, discarded, or a directory that was
// already gone. Each names the branch as kept, because that is the promise.
func TestRemovedWorktreeSentencesSayWhereTheWorkWent(t *testing.T) {
	stashed := removedWorktree{Session: "api-x", Branch: "x", Path: "/wt/x", Changes: 2, Stashed: true, StashMessage: "tuios: x", SessionKilled: true}.sentences()
	for _, want := range []string{"Removed worktree /wt/x. Branch x is kept.", "2 uncommitted changes are in git stash as 'tuios: x'.", "Killed session 'api-x'."} {
		if !strings.Contains(stashed, want) {
			t.Errorf("stashed sentences lack %q:\n%s", want, stashed)
		}
	}
	discarded := removedWorktree{Session: "api-x", Branch: "x", Path: "/wt/x", Changes: 1, Discarded: true}.sentences()
	for _, want := range []string{"1 uncommitted change was discarded.", "Session 'api-x' is still running."} {
		if !strings.Contains(discarded, want) {
			t.Errorf("discarded sentences lack %q:\n%s", want, discarded)
		}
	}
	gone := removedWorktree{Session: "api-x", Branch: "x", Gone: true, Note: "The directory was already gone.", SessionKilled: true}.sentences()
	if !strings.HasPrefix(gone, "The directory was already gone.") || strings.Contains(gone, "Removed worktree") {
		t.Errorf("gone sentences claim a removal:\n%s", gone)
	}
}

// TestFanCallerEnvSendsPathAndWhatWasAskedFor: PATH always goes, NAME takes
// this process's value, NAME=VALUE sets one, and a NAME this process does not
// have is refused rather than sent empty.
func TestFanCallerEnvSendsPathAndWhatWasAskedFor(t *testing.T) {
	vars := map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_API_KEY": "k", "EMPTY": ""}
	getenv := func(k string) string { return vars[k] }
	environ := []string{"PATH=/opt/bin:/usr/bin", "ANTHROPIC_API_KEY=k", "EMPTY="}
	env, err := fanCallerEnv([]string{"ANTHROPIC_API_KEY", "MODE=fast", "EMPTY"}, getenv, environ)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_API_KEY": "k", "MODE": "fast", "EMPTY": ""}
	if len(env) != len(want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("env[%s] = %q, want %q", k, got, v)
		}
	}
	if _, err := fanCallerEnv([]string{"NOT_SET_HERE"}, getenv, environ); err == nil {
		t.Error("an --env name this shell does not have was sent")
	}
}

// TestFanTakesSeveralAgentsAndPromptsOnTheCommandLine: --agent splits on
// commas and repeats, and --prompt replaces the prompt argument.
func TestFanTakesSeveralAgentsAndPromptsOnTheCommandLine(t *testing.T) {
	cmd := newFanCommand()
	if err := cmd.ParseFlags([]string{"--agent", "claude,codex --model o5", "--agent", "gemini", "--prompt", "a", "--prompt", "b"}); err != nil {
		t.Fatal(err)
	}
	agents, _ := cmd.Flags().GetStringSlice("agent")
	if strings.Join(agents, "|") != "claude|codex --model o5|gemini" {
		t.Errorf("agents = %q", agents)
	}
	prompts, _ := cmd.Flags().GetStringArray("prompt")
	if strings.Join(prompts, "|") != "a|b" {
		t.Errorf("prompts = %q", prompts)
	}
	if err := cmd.RunE(cmd, []string{"2", "one prompt for all"}); err == nil || !strings.Contains(err.Error(), "takes no prompt argument") {
		t.Errorf("--prompt with a prompt argument: %v", err)
	}
}

// TestWorktreeTableShowsGoneAndPromptStatus pins the two columns a person
// scans first when watching a fan-out: what became of the prompt, and whether
// the directory is still there.
func TestWorktreeTableShowsGoneAndPromptStatus(t *testing.T) {
	two := 2
	out := renderWorktreeTable([]worktreeRow{
		{Session: "api-a", Repo: "api", Branch: "fan/a", State: "working", PromptStatus: "sent", Changes: &two},
		{Session: "api-b", Repo: "api", Branch: "fan/b", State: "none", PromptStatus: "not_sent", Gone: true},
		{Session: "api-c", Repo: "api", Branch: "fan/c", State: "idle", PromptStatus: "stalled"},
	})
	for _, want := range []string{"api-a", "fan/a", "working", "sent", "gone", "not sent", "stalled"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}
