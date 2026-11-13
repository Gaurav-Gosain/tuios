package harness

import (
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

// Any reports whether the block offers anything at all.
func (a *Answers) Any() bool {
	return a.Approve != nil || a.ApproveAlways != nil || a.Deny != nil || a.Choose != "" || a.Text
}

// check validates the block of one rule. block is "screen", "title" or
// "notify", and state is the rule's state.
func (a *Answers) check(block, state string) error {
	if !a.Any() {
		return nil
	}
	if block == "notify" {
		return errorf("answers: a notify rule reads a notification, which has nothing on the screen to answer")
	}
	if state != "needs_input" {
		return errorf("answers: only a needs_input rule reads a prompt, and this rule says %s", state)
	}
	for _, e := range []struct {
		name string
		ans  *Answer
	}{{ActionApprove, a.Approve}, {ActionApproveAlways, a.ApproveAlways}, {ActionDeny, a.Deny}} {
		if e.ans == nil {
			continue
		}
		if err := e.ans.check(block); err != nil {
			return errorf("answers.%s: %w", e.name, err)
		}
	}
	switch a.Choose = strings.ToLower(strings.TrimSpace(a.Choose)); a.Choose {
	case "", ChooseDigit:
	default:
		return errorf("answers.choose %q (digit, or leave it out)", a.Choose)
	}
	if a.Choose != "" && block == "title" {
		return errorf("answers.choose: a title rule has no options on the screen to choose from")
	}
	return nil
}

// check validates one answer.
func (a *Answer) check(block string) error {
	a.Option = strings.ToLower(strings.TrimSpace(a.Option))
	if len(a.Keys) == 0 && a.Option == "" {
		return errorf("names neither keys nor an option")
	}
	if a.Option != "" && block == "title" {
		return errorf("option: a title rule has no options on the screen to match")
	}
	if len(a.Keys) > maxAnswerKeys {
		return errorf("%d keys, limit %d", len(a.Keys), maxAnswerKeys)
	}
	if _, err := a.KeyBytes(); err != nil {
		return err
	}
	return nil
}

// KeyBytes is Keys as the bytes they send, in order.
func (a *Answer) KeyBytes() ([]byte, error) {
	var out []byte
	for _, k := range a.Keys {
		b, ok := AnswerKeyBytes(k)
		if !ok {
			return nil, errorf("unknown key %q (enter, esc, tab, space, up, down, left, right, backspace, or one printable character)", k)
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
