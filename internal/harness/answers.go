package harness

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"tuios.dev/harness"
)

// The answer to a prompt: which keys a reply presses, read from the rule's
// [answers] block (harness.Answers) and the numbered options on the screen.

// AnswerActions lists the actions in the order a peek offers them.
var AnswerActions = []string{ActionApprove, ActionApproveAlways, ActionDeny, ActionChoose, ActionText}

// Option is one numbered choice a prompt shows.
type Option struct {
	N     int    `json:"n"`
	Label string `json:"label"`
}

// Prompt is a prompt a needs_input rule reads on a pane right now, and what
// may answer it.
type Prompt struct {
	// Source is "screen" or "title": which rules read it.
	Source string
	// Rule is the rule's index in its block.
	Rule int
	// Kind is approval or question.
	Kind string
	// Message is the prompt line the rule matched, or the rule's message.
	Message string
	// Lines is what the rule read: its region of the screen, or the title.
	Lines []string
	// Options are the numbered choices at the bottom of the screen, in order,
	// empty when none are there.
	Options []Option

	answers *harness.Answers
}

// Prompt sources.
const (
	PromptSourceScreen = "screen"
	PromptSourceTitle  = "title"
)

// WithoutAnswers is the prompt with no answers offered.
func (p Prompt) WithoutAnswers() Prompt {
	p.answers = nil
	return p
}

// Answerable reports whether the rule that read the prompt declares any answer.
func (p *Prompt) Answerable() bool {
	return p.answers != nil && p.answers.Any()
}

// Actions lists the actions that can answer the prompt as it is on the screen
// now: an answer bound to an option is left out while that option is not
// shown, and choose is left out when no numbered options are shown.
func (p *Prompt) Actions() []string {
	if !p.Answerable() {
		return nil
	}
	var out []string
	for _, action := range AnswerActions {
		if _, err := p.Resolve(action, p.sampleValue(action)); err == nil {
			out = append(out, action)
		}
	}
	return out
}

// sampleValue is a value that resolves for an action when anything does, so
// Actions can ask Resolve rather than repeat its rules.
func (p *Prompt) sampleValue(action string) string {
	switch action {
	case ActionChoose:
		if len(p.Options) > 0 {
			return strconv.Itoa(p.Options[0].N)
		}
	case ActionText:
		return "x"
	}
	return ""
}

// Reply is what answering a prompt sends: Keys to write to the pane as they
// are, or Text to paste and submit as a prompt. Exactly one is set. Sent says
// in words what was pressed, for the caller and the log.
type Reply struct {
	Keys []byte
	Text string
	Sent string
}

// ErrNoAnswer is the error Resolve returns for an action the prompt does not
// offer, with the reason in words.
type ErrNoAnswer struct{ Reason string }

func (e ErrNoAnswer) Error() string { return e.Reason }

// Resolve says what answering the prompt with action and value would send, or
// why it cannot be answered that way.
func (p *Prompt) Resolve(action, value string) (Reply, error) {
	if !p.Answerable() {
		return Reply{}, ErrNoAnswer{"the rule that reads this prompt declares no answers"}
	}
	a := p.answers
	switch action {
	case ActionApprove, ActionApproveAlways, ActionDeny:
		ans := map[string]*Answer{ActionApprove: a.Approve, ActionApproveAlways: a.ApproveAlways, ActionDeny: a.Deny}[action]
		if ans == nil {
			return Reply{}, ErrNoAnswer{"this prompt has no " + action + " answer"}
		}
		return p.resolveAnswer(action, ans)
	case ActionChoose:
		if a.Choose != ChooseDigit {
			return Reply{}, ErrNoAnswer{"this prompt takes no numbered choice"}
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			return Reply{}, ErrNoAnswer{"choose needs the number of an option as its value"}
		}
		for _, o := range p.Options {
			if o.N == n {
				return Reply{Keys: []byte(strconv.Itoa(n)), Sent: strconv.Itoa(n)}, nil
			}
		}
		if len(p.Options) == 0 {
			return Reply{}, ErrNoAnswer{"no numbered options are on the screen"}
		}
		return Reply{}, ErrNoAnswer{"no option " + strconv.Itoa(n) + " is on the screen"}
	case ActionText:
		if !a.Text {
			return Reply{}, ErrNoAnswer{"this prompt takes no typed answer"}
		}
		if strings.TrimSpace(value) == "" {
			return Reply{}, ErrNoAnswer{"text needs the answer as its value"}
		}
		return Reply{Text: value, Sent: "text"}, nil
	}
	return Reply{}, ErrNoAnswer{"unknown action " + strconv.Quote(action)}
}

// resolveAnswer is Resolve for approve, approve_always and deny.
func (p *Prompt) resolveAnswer(action string, ans *harness.Answer) (Reply, error) {
	digit := 0
	if want := strings.ToLower(strings.TrimSpace(ans.Option)); want != "" {
		for _, o := range p.Options {
			if strings.HasPrefix(strings.ToLower(o.Label), want) {
				digit = o.N
				break
			}
		}
		if digit == 0 {
			return Reply{}, ErrNoAnswer{"no option starting " + strconv.Quote(ans.Option) + " is on the screen, so " + action + " is not offered"}
		}
	}
	if len(ans.Keys) > 0 {
		keys, err := ans.KeyBytes()
		if err != nil {
			return Reply{}, ErrNoAnswer{err.Error()}
		}
		return Reply{Keys: keys, Sent: strings.Join(ans.Keys, " ")}, nil
	}
	return Reply{Keys: []byte(strconv.Itoa(digit)), Sent: strconv.Itoa(digit)}, nil
}

// ScreenPrompt reads the prompt a harness's screen rules find on a pane. tail
// is what the rules read, the pane's last ScreenLines(id) non-empty lines, and
// context is a longer tail the numbered options are read from, since a rule
// can read only the footer of a menu whose options sit above it. ok is false
// when no needs_input rule is the best match: the pane shows no prompt these
// rules can read.
func (r *Registry) ScreenPrompt(id string, tail, context []string) (Prompt, bool) {
	state, rule, ok := r.Classify(id, tail)
	if !ok || state != "needs_input" {
		return Prompt{}, false
	}
	m := r.Lookup(id)
	rl := &m.Screen.Rule[rule]
	lines := r.RuleLines(id, rule, tail)
	options, first := parseOptions(context)
	// A rule can read only the footer of a menu. The prompt a person reads is
	// the menu and the question over it, so when the options reach further up
	// than the region, the lines run from a little above the first option.
	if first >= 0 {
		if start := max(first-linesAboveOptions, 0); len(context)-start > len(lines) {
			lines = context[start:]
		}
	}
	answers := &rl.Answers
	if rl.Show == harness.ShowDialog {
		// The answers press keys into the dialog, so they are offered only
		// while the one dialog the rule reads is on the screen. RulePrompt
		// reads the same box.
		if inside, ok := r.RuleDialog(id, rule, tail); ok {
			lines = inside
		} else {
			answers = nil
		}
	}
	msg := r.RulePrompt(id, rule, tail)
	if msg == "" {
		msg = r.RuleMessage(id, rule)
	}
	return Prompt{
		Source:  PromptSourceScreen,
		Rule:    rule,
		Kind:    r.RuleKind(id, rule),
		Message: msg,
		Lines:   append([]string(nil), lines...),
		Options: options,
		answers: answers,
	}, true
}

// linesAboveOptions is how many lines over a menu's first option belong to
// the prompt: the question, and the command or file it asks about.
const linesAboveOptions = 6

// TitlePrompt reads the prompt a harness's title rules find in a pane's title.
func (r *Registry) TitlePrompt(id, title string) (Prompt, bool) {
	if title == "" {
		return Prompt{}, false
	}
	state, rule, ok := r.ClassifyTitle(id, title)
	if !ok || state != "needs_input" {
		return Prompt{}, false
	}
	m := r.Lookup(id)
	rl := &m.Title.Rule[rule]
	return Prompt{
		Source:  PromptSourceTitle,
		Rule:    rule,
		Kind:    r.TitleRuleKind(id, rule),
		Message: rl.Message,
		Lines:   []string{title},
		answers: &rl.Answers,
	}, true
}

// optionLine matches one numbered option: an optional cursor mark, the
// number with a dot or a parenthesis after it ("1." as Claude Code draws
// it) or in brackets ("[1]" as Kimi Code CLI draws it), and the label.
var optionLine = lazyre.New(`^(?:[❯›>▶→*]\s*)?(?:([0-9]{1,2})[.)]|\[([0-9]{1,2})\])\s+(\S.*)$`)

// maxOptionGap is how many lines that are not options may sit between two
// options of one menu: a label that wraps, or a description under it.
const maxOptionGap = 3

// parseOptions reads the numbered menu nearest the bottom of lines: options
// numbered 1 to N with at most maxOptionGap other lines between two of them.
// A run that does not count down to 1 is not a menu and gives nothing, which
// is what keeps a numbered list in the transcript above from posing as one.
// It also returns the index of option 1's line, -1 when there is no menu.
func parseOptions(lines []string) ([]Option, int) {
	var out []Option
	want, gap, first := 0, 0, -1
	for i, line := range slices.Backward(lines) {
		n, label, ok := parseOptionLine(line)
		if !ok {
			if len(out) > 0 {
				if gap++; gap > maxOptionGap {
					break
				}
			}
			continue
		}
		if len(out) > 0 && n != want {
			break
		}
		out = append(out, Option{N: n, Label: label})
		want, gap, first = n-1, 0, i
		if want == 0 {
			break
		}
	}
	if len(out) == 0 || out[len(out)-1].N != 1 {
		return nil, -1
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, first
}

// parseOptionLine reads one line as an option, with the box a TUI draws around
// a menu taken off first.
func parseOptionLine(line string) (int, string, bool) {
	trimmed := strings.TrimFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= 0x2500 && r <= 0x259f)
	})
	m := optionLine().FindStringSubmatch(trimmed)
	if m == nil {
		return 0, "", false
	}
	num := m[1]
	if num == "" {
		num = m[2]
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	label := CleanPromptLine(m[3])
	if label == "" {
		return 0, "", false
	}
	return n, label, true
}
