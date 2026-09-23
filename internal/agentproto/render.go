package agentproto

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// The transcript: what the pane shows of the conversation.
//
// Everything the agent sends is text the model or the agent chose, and the pane
// is a terminal, so every string is cleaned before it is written: no escape
// sequence and no control character other than a line break and a tab reaches
// the pane. Without that an agent could print an OSC sequence the daemon reads
// as the pane's agent state, a notification, a title or a clipboard write, all
// of it unseen, in its reply.
//
// Colour is never the only signal: a tool call's state is a word, a diff line
// keeps its + or -, and a plan step its [x], [>] or [ ].

// SGR sequences the transcript uses.
const (
	sgrReset  = "\x1b[0m"
	sgrBold   = "\x1b[1m"
	sgrDim    = "\x1b[2m"
	sgrRed    = "\x1b[31m"
	sgrGreen  = "\x1b[32m"
	sgrYellow = "\x1b[33m"
	sgrCyan   = "\x1b[36m"
)

// Bounds on what one event may put on screen.
const (
	maxDiffLines   = 200
	maxToolText    = 5
	maxLineDiffOps = 4_000_000
)

// clean removes every escape sequence and control character from s except the
// line break and the tab, and replaces invalid UTF-8. A carriage return is
// dropped, so a CRLF is a line break and a bare CR cannot move the cursor back
// over what was written.
func clean(s string) string {
	if !strings.ContainsFunc(s, isUnsafe) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToValidUTF8(s, string(utf8.RuneError)) {
		if isUnsafe(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isUnsafe is a rune clean drops: C0 controls but tab and line feed, DEL, the
// C1 controls, and the bidi format characters that make a line read as
// something other than what it holds.
func isUnsafe(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r < 0xa0:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// oneLine is clean, with line breaks and tabs turned into spaces and runs of
// space collapsed.
func oneLine(s string) string {
	return strings.Join(strings.Fields(clean(s)), " ")
}

// renderer turns events into transcript text. It holds what it has already
// shown, so a tool call is shown when it starts and when it ends, not on every
// update, and a plan only when it changes.
type renderer struct {
	// midLine is true when the last text written did not end a line.
	midLine bool
	// thought is true while reasoning is streaming, so the switch to the reply
	// starts a new line.
	thought bool
	tools   map[string]toolShown
	plan    string
}

// toolShown is what was last shown of a tool call.
type toolShown struct {
	status string
	diffs  string
}

func newRenderer() *renderer {
	return &renderer{tools: make(map[string]toolShown)}
}

// lineStart ends a line left open, so what follows starts on its own.
func (r *renderer) lineStart(b *strings.Builder) {
	if r.midLine {
		b.WriteString("\n")
		r.midLine = false
	}
}

// block writes whole lines.
func (r *renderer) block(b *strings.Builder, s string) {
	r.lineStart(b)
	r.thought = false
	b.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		b.WriteString("\n")
	}
}

// text renders one event. It returns the text to write, with \n line breaks.
func (r *renderer) event(ev Event) string {
	var b strings.Builder
	switch e := ev.(type) {
	case Text:
		s := clean(e.Text)
		if s == "" {
			break
		}
		if e.Thought != r.thought {
			r.lineStart(&b)
			r.thought = e.Thought
		}
		if e.Thought {
			// Dim each piece on its own, so a line break inside it does
			// not leave the style open across a redraw.
			for i, part := range strings.Split(s, "\n") {
				if i > 0 {
					b.WriteString("\n")
				}
				if part != "" {
					b.WriteString(sgrDim + part + sgrReset)
				}
			}
		} else {
			b.WriteString(s)
		}
		r.midLine = !strings.HasSuffix(s, "\n")
	case Tool:
		r.tool(&b, e)
	case Plan:
		r.planBlock(&b, e)
	case Notice:
		colour, label := sgrYellow, "note"
		if e.Error {
			colour, label = sgrRed, "error"
		}
		r.block(&b, colour+label+sgrReset+": "+oneLine(e.Text))
	}
	return b.String()
}

// statusWord is how a tool status reads in the transcript.
func statusWord(s string) (string, string) {
	switch s {
	case ToolRunning:
		return "running", sgrCyan
	case ToolDone:
		return "done", sgrGreen
	case ToolFailed:
		return "failed", sgrRed
	}
	return "pending", sgrYellow
}

// toolLabel is a tool call as one line: its kind and its title.
func toolLabel(t Tool) string {
	title := oneLine(t.Title)
	if title == "" {
		title = oneLine(t.ID)
	}
	if k := oneLine(t.Kind); k != "" {
		return k + ": " + title
	}
	return title
}

func (r *renderer) tool(b *strings.Builder, t Tool) {
	prev := r.tools[t.ID]
	diffs := diffKey(t.Diffs)
	if prev.status == t.Status && prev.diffs == diffs {
		return
	}
	// Pending and running read the same to a person watching: the call is
	// under way. Only the change to an end is worth a second line.
	started := prev.status != ""
	ending := t.Status == ToolDone || t.Status == ToolFailed
	if !started || (ending && prev.status != t.Status) {
		word, colour := statusWord(t.Status)
		if !started && !ending {
			word, colour = statusWord(ToolRunning)
		}
		r.block(b, colour+"["+word+"]"+sgrReset+" "+toolLabel(t))
		if ending && t.Text != "" {
			r.block(b, sgrDim+indentLines(clipLines(clean(t.Text), maxToolText))+sgrReset)
		}
	}
	if diffs != "" && diffs != prev.diffs {
		r.lineStart(b)
		b.WriteString(renderDiffs(t.Diffs))
	}
	r.tools[t.ID] = toolShown{status: t.Status, diffs: diffs}
}

func (r *renderer) planBlock(b *strings.Builder, p Plan) {
	var s strings.Builder
	s.WriteString(sgrBold + "plan" + sgrReset + "\n")
	for _, e := range p.Entries {
		mark := "[ ]"
		switch e.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[>]"
		}
		s.WriteString("  " + mark + " " + oneLine(e.Content) + "\n")
	}
	if s.String() == r.plan {
		return
	}
	r.plan = s.String()
	r.block(b, r.plan)
}

// prompt is the person's prompt as the transcript shows it.
func (r *renderer) prompt(text string) string {
	var b strings.Builder
	r.lineStart(&b)
	b.WriteString("\n")
	lines := strings.Split(clean(text), "\n")
	b.WriteString(sgrBold + "you" + sgrReset + "  " + lines[0] + "\n")
	for _, l := range lines[1:] {
		b.WriteString("     " + l + "\n")
	}
	b.WriteString("\n")
	r.thought = false
	return b.String()
}

// permission is a permission request as the pane shows it: what is asked,
// its context and diffs, and the keys that answer it.
func (r *renderer) permission(p *Permission) string {
	var b strings.Builder
	r.lineStart(&b)
	r.thought = false
	b.WriteString("\n" + sgrBold + sgrYellow + "permission" + sgrReset + " " + toolLabel(p.Tool) + "\n")
	if d := oneLine(p.Detail); d != "" {
		b.WriteString("  " + d + "\n")
	}
	if reason := oneLine(p.Reason); reason != "" {
		b.WriteString("  reason: " + reason + "\n")
	}
	// Input the title does not show is shown under it, so the pane shows the
	// whole call even when the Inbox cannot.
	for _, k := range slices.Sorted(maps.Keys(p.Tool.Input)) {
		if v := p.Tool.Input[k]; v != "" && !strings.Contains(p.Tool.Title, v) {
			b.WriteString("  " + oneLine(k) + ": " + oneLine(v) + "\n")
		}
	}
	if p.Tool.Text != "" {
		b.WriteString(sgrDim + indentLines(clipLines(clean(p.Tool.Text), maxToolText)) + sgrReset + "\n")
	}
	if len(p.Tool.Diffs) > 0 {
		b.WriteString(renderDiffs(p.Tool.Diffs))
		r.tools[p.Tool.ID] = toolShown{status: r.tools[p.Tool.ID].status, diffs: diffKey(p.Tool.Diffs)}
	}
	b.WriteString("  " + optionKeys(p) + "\n")
	return b.String()
}

// optionKeys is the line that says which key picks which answer.
func optionKeys(p *Permission) string {
	parts := make([]string, 0, len(p.Options)+1)
	for i, o := range p.Options {
		if i >= 9 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s%d%s %s", sgrBold, i+1, sgrReset, oneLine(o.Label)))
	}
	parts = append(parts, sgrBold+"Ctrl+C"+sgrReset+" cancel the turn")
	return strings.Join(parts, "   ")
}

// answered is the line under a permission once it is answered.
func (r *renderer) answered(p *Permission, i int, how string) string {
	var b strings.Builder
	label := "cancelled"
	if i >= 0 && i < len(p.Options) {
		label = oneLine(p.Options[i].Label)
	}
	r.block(&b, sgrDim+"answered: "+sgrReset+label+sgrDim+" ("+oneLine(how)+")"+sgrReset)
	return b.String()
}

// turnEnd is the line a turn ends with.
func (r *renderer) turnEnd(res TurnResult) string {
	var b strings.Builder
	switch res.Stop {
	case StopFailed, StopRefused:
		msg := "turn " + res.Stop
		if d := oneLine(res.Detail); d != "" {
			msg += ": " + d
		}
		r.block(&b, sgrRed+msg+sgrReset)
	default:
		msg := "turn " + res.Stop
		if d := oneLine(res.Detail); d != "" {
			msg += ": " + d
		}
		r.block(&b, sgrDim+msg+sgrReset)
	}
	return b.String()
}

// line is a line of the client's own, dimmed.
func (r *renderer) line(s string) string {
	var b strings.Builder
	r.block(&b, sgrDim+s+sgrReset)
	return b.String()
}

func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func clipLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n(%d more lines)", len(lines)-n)
}

// InboxLine is the one line the Inbox may offer to answer a permission from,
// or empty when the Inbox may not answer it and only the pane can.
//
// The person answers from this line alone, so it has to be the whole request,
// the rule `tuios agent-hook` follows (internal/integration/approval.go): a
// call with a diff, output, a terminal or anything else the line cannot show is
// answered in the pane. The title is the whole call only when every string the
// call's input holds appears in it, except a description, which says what the
// call is for and not what it does; input that is not a string rules the Inbox
// out. The line must also survive the Inbox's cleaning unchanged, and the
// request must offer to allow it once.
func InboxLine(p *Permission) string {
	t := p.Tool
	if t.Title == "" || len(t.Diffs) > 0 || t.Text != "" || t.Terminal || t.OtherInput {
		return ""
	}
	if p.Pick(DecisionOnce) < 0 {
		return ""
	}
	for k, v := range t.Input {
		if k == "description" || v == "" {
			continue
		}
		if !strings.Contains(t.Title, v) {
			return ""
		}
	}
	kind := t.Kind
	if kind == "" {
		kind = "tool"
	}
	line := "approve " + kind + ": " + t.Title
	if integration.Clip(line) != line || !printableLine(line) {
		return ""
	}
	return line
}

// StateLine is the message the pane's needs_input report carries: the Inbox
// line when there is one, and otherwise the same words cleaned and clipped,
// which the Inbox shows without offering to answer.
func StateLine(p *Permission) string {
	if line := InboxLine(p); line != "" {
		return line
	}
	kind := p.Tool.Kind
	if kind == "" {
		kind = "tool"
	}
	return integration.Clip("approve " + kind + ": " + oneLine(firstOf(p.Tool.Title, p.Tool.ID)))
}

// printableLine reports whether every rune of s is visible or a plain space.
func printableLine(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
