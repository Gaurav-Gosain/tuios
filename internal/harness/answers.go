package harness

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Answers: how a person answers a prompt a needs_input rule reads, without
// attaching to the pane.
//
// A rule that recognises a prompt can also say which keys answer it. Claude
// Code's permission menu takes 1 for yes, 2 for yes and do not ask again, and
// esc for no; its question forms take the digit of the chosen option. Writing
// that down next to the rule that reads the menu keeps the two together, so a
// release that changes the menu breaks the rule and the answer at the same
// time, and a broken rule answers nothing.
//
// An answer is bound to what is on the screen, not only to a key. An answer
// may name an option by the start of its label ("yes, and don't ask again"),
// and it is then offered only while a numbered option with that label is on
// the screen. Without the label check, approve_always = "2" would press No on
// a menu that has only Yes and No. The respond verb in the daemon adds the
// other half: it reads the screen again right before it presses anything, and
// refuses when the prompt is not the one the caller read.
//
// The block, under a screen or title rule:
//
//	[screen.rule.answers]
//	approve        = { option = "yes" }
//	approve_always = { option = "yes, and don't ask again" }
//	deny           = { keys = ["esc"] }
//	choose         = "digit"
//	text           = true
//
// approve, approve_always and deny each take keys, option, or both: with
// option alone the option's digit is pressed, and with keys the keys are
// pressed once the option is found. choose = "digit" lets a caller pick any
// numbered option on the screen by its number. text = true lets a caller type
// a free answer, which is pasted and submitted the way a prompt is.

// Answer actions, as the respond verb and the peek result name them.
const (
	ActionApprove       = "approve"
	ActionApproveAlways = "approve_always"
	ActionDeny          = "deny"
	ActionChoose        = "choose"
	ActionText          = "text"
)

// AnswerActions lists the actions in the order a peek offers them.
var AnswerActions = []string{ActionApprove, ActionApproveAlways, ActionDeny, ActionChoose, ActionText}

// ChooseDigit is the one choose mode: the option's number, typed as digits.
const ChooseDigit = "digit"

// maxAnswerKeys bounds the keys one answer presses. An answer is a key or two;
// a long sequence in a manifest is a macro, and a macro typed into a prompt is
// what this feature exists to avoid.
const maxAnswerKeys = 8

// Answers is a rule's [answers] block. The zero value answers nothing.
type Answers struct {
	Approve       *Answer `toml:"approve"`
	ApproveAlways *Answer `toml:"approve_always"`
	Deny          *Answer `toml:"deny"`
	// Choose is "digit" to let a caller pick a numbered option by number, and
	// empty to offer no choice.
	Choose string `toml:"choose"`
	// Text lets a caller answer in words.
	Text bool `toml:"text"`
}

// Answer is one of approve, approve_always and deny.
type Answer struct {
	// Keys are key names: enter, esc, tab, space, up, down, left, right,
	// backspace, or one printable character.
	Keys []string `toml:"keys"`
	// Option is the start of a numbered option's label, matched without case.
	// The answer is offered only while such an option is on the screen.
	Option string `toml:"option"`
}

// any reports whether the block offers anything at all.
func (a *Answers) any() bool {
	return a.Approve != nil || a.ApproveAlways != nil || a.Deny != nil || a.Choose != "" || a.Text
}

// check validates the block of one rule. block is "screen", "title" or
// "notify", and state is the rule's state.
func (a *Answers) check(block, state string) error {
	if !a.any() {
		return nil
	}
	if block == "notify" {
		return fmt.Errorf("answers: a notify rule reads a notification, which has nothing on the screen to answer")
	}
	if state != "needs_input" {
		return fmt.Errorf("answers: only a needs_input rule reads a prompt, and this rule says %s", state)
	}
	for _, e := range []struct {
		name string
		ans  *Answer
	}{{ActionApprove, a.Approve}, {ActionApproveAlways, a.ApproveAlways}, {ActionDeny, a.Deny}} {
		if e.ans == nil {
			continue
		}
		if err := e.ans.check(block); err != nil {
			return fmt.Errorf("answers.%s: %w", e.name, err)
		}
	}
	switch a.Choose = strings.ToLower(strings.TrimSpace(a.Choose)); a.Choose {
	case "", ChooseDigit:
	default:
		return fmt.Errorf("answers.choose %q (digit, or leave it out)", a.Choose)
	}
	if a.Choose != "" && block == "title" {
		return fmt.Errorf("answers.choose: a title rule has no options on the screen to choose from")
	}
	return nil
}

// check validates one answer.
func (a *Answer) check(block string) error {
	a.Option = strings.ToLower(strings.TrimSpace(a.Option))
	if len(a.Keys) == 0 && a.Option == "" {
		return fmt.Errorf("names neither keys nor an option")
	}
	if a.Option != "" && block == "title" {
		return fmt.Errorf("option: a title rule has no options on the screen to match")
	}
	if len(a.Keys) > maxAnswerKeys {
		return fmt.Errorf("%d keys, limit %d", len(a.Keys), maxAnswerKeys)
	}
	if _, err := a.keyBytes(); err != nil {
		return err
	}
	return nil
}

// keyBytes is Keys as the bytes they send.
func (a *Answer) keyBytes() ([]byte, error) {
	var out []byte
	for _, k := range a.Keys {
		b, ok := AnswerKeyBytes(k)
		if !ok {
			return nil, fmt.Errorf("unknown key %q (enter, esc, tab, space, up, down, left, right, backspace, or one printable character)", k)
		}
		out = append(out, b...)
	}
	return out, nil
}

// answerKeyNames maps the named keys an answer may press to their bytes, as
// a terminal in raw mode sends them.
var answerKeyNames = map[string]string{
	"enter":     "\r",
	"esc":       "\x1b",
	"escape":    "\x1b",
	"tab":       "\t",
	"space":     " ",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"backspace": "\x7f",
}

// AnswerKeyBytes returns the bytes a key name sends: a named key, or one
// printable character sent as itself. A single character keeps its case,
// since y and Y can mean different things to a TUI.
func AnswerKeyBytes(name string) ([]byte, bool) {
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		if r != utf8.RuneError && unicode.IsPrint(r) && r != ' ' {
			return []byte(name), true
		}
	}
	if b, ok := answerKeyNames[strings.ToLower(strings.TrimSpace(name))]; ok {
		return []byte(b), true
	}
	return nil, false
}

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

	answers *Answers
}

// Prompt sources.
const (
	PromptSourceScreen = "screen"
	PromptSourceTitle  = "title"
)

// Answerable reports whether the rule that read the prompt declares any answer.
func (p *Prompt) Answerable() bool {
	return p.answers != nil && p.answers.any()
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
func (p *Prompt) resolveAnswer(action string, ans *Answer) (Reply, error) {
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
		keys, err := ans.keyBytes()
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
	lines := regionLines(tail, rl.Region)
	options, first := parseOptions(context)
	// A rule can read only the footer of a menu. The prompt a person reads is
	// the menu and the question over it, so when the options reach further up
	// than the region, the lines run from a little above the first option.
	if first >= 0 {
		if start := max(first-linesAboveOptions, 0); len(context)-start > len(lines) {
			lines = context[start:]
		}
	}
	msg := r.RulePrompt(id, rule, tail)
	if msg == "" {
		msg = r.RuleMessage(id, rule)
	}
	return Prompt{
		Source:  PromptSourceScreen,
		Rule:    rule,
		Kind:    ruleKind(rl),
		Message: msg,
		Lines:   append([]string(nil), lines...),
		Options: options,
		answers: &rl.Answers,
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
		Kind:    ruleKind(rl),
		Message: rl.Message,
		Lines:   []string{title},
		answers: &rl.Answers,
	}, true
}

// optionLine matches one numbered option: an optional cursor mark, the
// number, a dot or a parenthesis, and the label.
var optionLine = regexp.MustCompile(`^(?:[❯›>▶→*]\s*)?([0-9]{1,2})[.)]\s+(\S.*)$`)

// maxOptionGap is how many lines that are not options may sit between two
// options of one menu: a label that wraps, or a description under it.
const maxOptionGap = 3

// ParseOptions reads the numbered menu nearest the bottom of lines: options
// numbered 1 to N with at most maxOptionGap other lines between two of them.
// A run that does not count down to 1 is not a menu and gives nothing, which
// is what keeps a numbered list in the transcript above from posing as one.
func ParseOptions(lines []string) []Option {
	out, _ := parseOptions(lines)
	return out
}

// parseOptions is ParseOptions with the index of option 1's line, -1 when
// there is no menu.
func parseOptions(lines []string) ([]Option, int) {
	var out []Option
	want, gap, first := 0, 0, -1
	for i := len(lines) - 1; i >= 0; i-- {
		n, label, ok := parseOptionLine(lines[i])
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
	m := optionLine.FindStringSubmatch(trimmed)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, "", false
	}
	label := CleanPromptLine(m[2])
	if label == "" {
		return 0, "", false
	}
	return n, label, true
}
