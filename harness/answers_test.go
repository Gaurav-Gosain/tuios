package harness

import (
	"strings"
	"testing"
)

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
