package harness

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// claudeApproval is Claude Code's permission menu as its screen tail reads.
var claudeApproval = []string{
	"────────────────────────────────────────",
	" Bash command",
	"   rm -rf build",
	" Do you want to proceed?",
	" ❯ 1. Yes",
	"   2. Yes, and don't ask again for rm commands in /src/app",
	"   3. No, and tell Claude what to do differently (esc)",
}

func bundledRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	return reg
}

func screenPrompt(t *testing.T, reg *Registry, id string, tail []string) Prompt {
	t.Helper()
	p, ok := reg.ScreenPrompt(id, tail, tail)
	if !ok {
		t.Fatalf("no prompt read on %q", tail)
	}
	return p
}

func sent(t *testing.T, p Prompt, action, value string) string {
	t.Helper()
	r, err := p.Resolve(action, value)
	if err != nil {
		t.Fatalf("%s %q: %v", action, value, err)
	}
	if r.Text != "" {
		return "text:" + r.Text
	}
	return fmt.Sprintf("%q", r.Keys)
}

// TestClaudeApprovalIsAnsweredByItsOwnKeys holds the bundled claude-code rule
// to the keys its menu takes: the digit of the option, and esc for no.
func TestClaudeApprovalIsAnsweredByItsOwnKeys(t *testing.T) {
	reg := bundledRegistry(t)
	p := screenPrompt(t, reg, "claude-code", claudeApproval)
	if p.Kind != PromptKindApproval || p.Source != PromptSourceScreen {
		t.Errorf("kind %q source %q", p.Kind, p.Source)
	}
	if len(p.Options) != 3 || p.Options[0].Label != "Yes" || p.Options[2].N != 3 {
		t.Fatalf("options %+v", p.Options)
	}
	if got := strings.Join(p.Actions(), ","); got != "approve,approve_always,deny,choose" {
		t.Errorf("actions %s", got)
	}
	for _, c := range []struct{ action, value, want string }{
		{ActionApprove, "", `"1"`},
		{ActionApproveAlways, "", `"2"`},
		{ActionDeny, "", `"\x1b"`},
		{ActionChoose, "3", `"3"`},
	} {
		if got := sent(t, p, c.action, c.value); got != c.want {
			t.Errorf("%s %s sends %s, want %s", c.action, c.value, got, c.want)
		}
	}
	for _, c := range []struct{ action, value string }{
		{ActionChoose, "4"},
		{ActionChoose, "one"},
		{ActionText, "use main"},
		{"shrug", ""},
	} {
		if _, err := p.Resolve(c.action, c.value); err == nil {
			t.Errorf("%s %q resolved on a menu that does not offer it", c.action, c.value)
		}
	}
}

// TestApproveAlwaysNeedsItsLabel is why an answer names a label and not only a
// key: on a menu with only Yes and No, 2 is No, so approve_always must not be
// offered at all.
func TestApproveAlwaysNeedsItsLabel(t *testing.T) {
	reg := bundledRegistry(t)
	p := screenPrompt(t, reg, "claude-code", []string{
		" Do you want to make this edit to main.go?",
		" ❯ 1. Yes",
		"   2. No, and tell Claude what to do differently (esc)",
	})
	if slices.Contains(p.Actions(), ActionApproveAlways) {
		t.Fatalf("approve_always offered on a Yes/No menu: %v", p.Actions())
	}
	if _, err := p.Resolve(ActionApproveAlways, ""); err == nil {
		t.Fatal("approve_always resolved on a menu whose option 2 is No")
	}
	if got := sent(t, p, ActionApprove, ""); got != `"1"` {
		t.Errorf("approve sends %s", got)
	}
}

// TestCodexApprovalPressesItsPrintedShortcuts: Codex prints each option's key,
// and the rule presses it once the label is there.
func TestCodexApprovalPressesItsPrintedShortcuts(t *testing.T) {
	reg := bundledRegistry(t)
	tail := []string{
		"Would you like to run the following command?",
		"$ go test ./...",
		"› 1. Yes, proceed (y)",
		"  2. Yes, and don't ask again for this command (a)",
		"  3. No, and tell Codex what to do differently (esc)",
		"Press enter to confirm or esc to cancel",
	}
	p := screenPrompt(t, reg, "codex", tail)
	// The rule reads the footer alone. The prompt a person reads is the whole
	// menu and the question over it.
	if got := strings.Join(p.Lines, "\n"); !strings.Contains(got, "$ go test ./...") || !strings.HasSuffix(got, "esc to cancel") {
		t.Errorf("the prompt's lines are %q", got)
	}
	for _, c := range []struct{ action, want string }{
		{ActionApprove, `"y"`}, {ActionApproveAlways, `"a"`}, {ActionDeny, `"\x1b"`},
	} {
		if got := sent(t, p, c.action, ""); got != c.want {
			t.Errorf("%s sends %s, want %s", c.action, got, c.want)
		}
	}
	// The same footer under a question form offers deny only.
	q := screenPrompt(t, reg, "codex", []string{
		"Which database should the service use?",
		"› 1. Postgres",
		"  2. SQLite",
		"Press enter to confirm or esc to cancel",
	})
	if got := strings.Join(q.Actions(), ","); got != "deny" {
		t.Errorf("a question form offers %s, want deny", got)
	}
}

// TestNoPromptNoAnswer: a screen no needs_input rule reads is not a prompt,
// and a rule that declares no answers answers nothing.
func TestNoPromptNoAnswer(t *testing.T) {
	reg := bundledRegistry(t)
	if _, ok := reg.ScreenPrompt("claude-code", []string{"────", "❯", "────"}, nil); ok {
		t.Error("an idle prompt box read as a prompt")
	}
	r := mustManifest(t, `
[screen]
enabled = true
[[screen.rule]]
state = "needs_input"
all = ["Proceed?"]
`)
	p, ok := r.ScreenPrompt("x-agent", []string{"Proceed?", "1. Yes"}, []string{"Proceed?", "1. Yes"})
	if !ok {
		t.Fatal("the rule did not read its prompt")
	}
	if p.Answerable() || len(p.Actions()) != 0 {
		t.Errorf("a rule with no answers offers %v", p.Actions())
	}
	if _, err := p.Resolve(ActionChoose, "1"); err == nil {
		t.Error("a rule with no answers resolved a choice")
	}
}

// TestTitleAnswersPressKeys: a title rule can answer with keys, and only keys,
// since a title shows no options.
func TestTitleAnswersPressKeys(t *testing.T) {
	r := mustManifest(t, `
[title]
enabled = true
[[title.rule]]
state = "needs_input"
any = ["Action Required"]
[title.rule.answers]
approve = { keys = ["y"] }
deny = { keys = ["n", "enter"] }
text = true
`)
	p, ok := r.TitlePrompt("x-agent", "Action Required - x")
	if !ok {
		t.Fatal("the title rule did not read the title")
	}
	if p.Source != PromptSourceTitle || p.Lines[0] != "Action Required - x" {
		t.Errorf("prompt %+v", p)
	}
	if got := sent(t, p, ActionDeny, ""); got != `"n\r"` {
		t.Errorf("deny sends %s", got)
	}
	if got := sent(t, p, ActionText, "go on"); got != "text:go on" {
		t.Errorf("text sends %s", got)
	}
	if _, ok := r.TitlePrompt("x-agent", "x"); ok {
		t.Error("a title the rule does not match read as a prompt")
	}
}

// TestAnswersAreCheckedAtLoad: a mistake in an answers block names the file
// at load, rather than pressing the wrong key later.
func TestAnswersAreCheckedAtLoad(t *testing.T) {
	cases := map[string]string{
		"on a working rule": `
[screen]
enabled = true
[[screen.rule]]
state = "working"
all = ["x"]
[screen.rule.answers]
deny = { keys = ["esc"] }
`,
		"an unknown key": `
[screen]
enabled = true
[[screen.rule]]
state = "needs_input"
all = ["x"]
[screen.rule.answers]
deny = { keys = ["hyper+q"] }
`,
		"an answer naming nothing": `
[screen]
enabled = true
[[screen.rule]]
state = "needs_input"
all = ["x"]
[screen.rule.answers]
approve = {}
`,
		"an unknown choose mode": `
[screen]
enabled = true
[[screen.rule]]
state = "needs_input"
all = ["x"]
[screen.rule.answers]
choose = "arrows"
`,
		"an option on a title rule": `
[title]
enabled = true
[[title.rule]]
state = "needs_input"
any = ["x"]
[title.rule.answers]
approve = { option = "yes" }
`,
		"answers on a notify rule": `
[notify]
enabled = true
[[notify.rule]]
state = "needs_input"
any = ["x"]
[notify.rule.answers]
deny = { keys = ["esc"] }
`,
		"too many keys": `
[screen]
enabled = true
[[screen.rule]]
state = "needs_input"
all = ["x"]
[screen.rule.answers]
deny = { keys = ["a","b","c","d","e","f","g","h","i"] }
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseManifest("x.toml", []byte(manifestHead+body)); err == nil {
				t.Fatal("the manifest loaded")
			} else if !strings.Contains(err.Error(), "answers") {
				t.Errorf("the error does not name the answers block: %v", err)
			}
		})
	}
}

// TestParseOptionsReadsTheMenuNearestTheBottom: a numbered list in the
// transcript is not the menu, and a run that does not count down to 1 is not
// a menu at all.
func TestParseOptionsReadsTheMenuNearestTheBottom(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"plain", []string{"Pick one", "❯ 1. Red", "  2. Green"}, "1 Red|2 Green"},
		{"transcript above", []string{"1. Fix the bug", "2. Add a test", "Proceed?", "│ ❯ 1. Yes │", "│   2. No  │"}, "1 Yes|2 No"},
		{"wrapped label", []string{"1. Yes", "2. Yes, and don't ask again for", "   commands in /src", "3. No", "Esc to cancel"}, "1 Yes|2 Yes, and don't ask again for|3 No"},
		{"no 1", []string{"2. Green", "3. Blue"}, ""},
		{"out of order", []string{"1. Red", "3. Blue"}, ""},
		{"too far apart", []string{"1. Red", "a", "b", "c", "d", "2. Green"}, ""},
		{"parenthesis", []string{"1) Red", "2) Green"}, "1 Red|2 Green"},
		{"none", []string{"nothing here"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, o := range ParseOptions(c.lines) {
				got = append(got, fmt.Sprintf("%d %s", o.N, o.Label))
			}
			if s := strings.Join(got, "|"); s != c.want {
				t.Errorf("got %q, want %q", s, c.want)
			}
		})
	}
}

func TestAnswerKeyBytes(t *testing.T) {
	for name, want := range map[string]string{"esc": "\x1b", "Enter": "\r", "y": "y", "Y": "Y", "2": "2", "tab": "\t"} {
		if got, ok := AnswerKeyBytes(name); !ok || string(got) != want {
			t.Errorf("%q gives %q %v, want %q", name, got, ok, want)
		}
	}
	for _, bad := range []string{"", "\x1b", "ctrl+c", " ", "yy"} {
		if _, ok := AnswerKeyBytes(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
