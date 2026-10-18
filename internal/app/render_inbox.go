package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// inboxWidth is the Inbox overlay's preferred inner width: a row carries a
// name, a summary, the session and the wait.
const inboxWidth = 72

// defaultInboxKeys is the shipped bindings, for a client built without a
// registry.
var defaultInboxKeys = sync.OnceValue(func() *config.KeybindRegistry {
	return config.NewKeybindRegistry(config.DefaultConfig())
})

// inboxKey is the key a hint names for an action of the Inbox, its peek or
// the mailbox: the first key the config binds it to, as the hint strips spell
// keys. Empty for an action bound to nothing, whose hint is left out.
func (m *OS) inboxKey(action string) string {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	for _, k := range reg.GetInboxKeys(action) {
		switch k = strings.TrimSpace(k); k {
		case "":
			continue
		case "enter":
			return overlay.EnterGlyph
		default:
			return k
		}
	}
	return ""
}

// inboxKeyOr is inboxKey with a word for an action bound to nothing, for a
// message that has to name some key.
func (m *OS) inboxKeyOr(action, fallback string) string {
	if k := m.inboxKey(action); k != "" {
		return k
	}
	return fallback
}

// pressFor is what to press for a prefix or mode action, chord included, as a
// message spells it ("ctrl+b i"), or fallback when the config binds it to
// nothing. For the rare message that names a key; it scans every binding.
func (m *OS) pressFor(action, fallback string) string {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	if presses := config.PressesByAction(reg)[action]; len(presses) > 0 {
		return presses[0]
	}
	return fallback
}

// keyHints builds a footer from action and label pairs, leaving out an action
// the config binds to nothing: a hint for a key that does nothing is worse than
// no hint.
func (m *OS) keyHints(pairs ...string) []overlay.Hint {
	var hints []overlay.Hint
	for i := 0; i+1 < len(pairs); i += 2 {
		if key := m.inboxKey(pairs[i]); key != "" {
			h := overlay.Hint{Key: key, Label: pairs[i+1]}
			if pairs[i] == config.ActionInboxClose {
				// The way out keeps its label on any key it is bound to.
				h.Priority = overlay.HintEssential
			}
			hints = append(hints, h)
		}
	}
	return hints
}

// inboxPeekMaxLines is how many prompt lines the peek shows at most. A prompt
// longer than the screen allows keeps its bottom, where the question and the
// options are.
const inboxPeekMaxLines = 14

// renderInboxPeek draws the peek: who waits and for how long, the prompt as
// the pane shows it behind a bar that marks it as the pane's text, its
// options, and the keys that answer it. What the peek says is in words; the
// colours only repeat it.
func (m *OS) renderInboxPeek(p *inboxPeek, now time.Time) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(inboxWidth)
	textW := max(width-2, 1)
	it := p.Item
	pk := p.Peek

	var body []string
	add := func(ink color.Color, s string) {
		for _, l := range wrapPlain(s, textW) {
			body = append(body, overlay.Style(bg).Foreground(ink).Render("  "+l))
		}
	}

	since := it.Since
	if pk != nil && pk.Blocked && pk.StateAt > 0 {
		since = pk.StateAt
	}
	what := inboxKindWords(it)
	if pk != nil && !pk.Blocked {
		what = "is " + sidebarStateWords(pk.State) + " now, not waiting on a prompt"
	}
	add(pal.Fg, inboxWho(it)+" in "+m.inboxWhere(it)+" "+what+sepWord()+"waited "+inboxWait(since, now))
	if it.Host == "" {
		if facts := m.agentFactsLine(it.Session, it.Window, it.Harness); facts != "" {
			add(pal.FgDim, facts)
		}
		for _, l := range programStatusLines(m.paneProgramStatus(it.Session, it.Window)) {
			add(pal.FgDim, l)
		}
		for _, l := range subagentDetailLines(m.paneSubagentList(it.Session, it.Window)) {
			add(pal.FgDim, l)
		}
	}
	if p.Note != "" {
		add(pal.Warning, p.Note)
	}

	bar := "│ "
	if overlay.UseASCII() {
		bar = "| "
	}
	hints := []overlay.Hint{}
	switch {
	case pk == nil && p.Loading:
		if overlay.ShowLoading(p.LoadingSince, now) {
			body = append(body, "")
			add(pal.FgDim, "Reading the prompt...")
		}
	case pk == nil:
	case !pk.Found:
		body = append(body, "")
		add(pal.FgDim, capitalize(printableTitle(pk.Reason))+".")
	default:
		body = append(body, "")
		lines := pk.Lines
		if len(lines) > inboxPeekMaxLines {
			lines = lines[len(lines)-inboxPeekMaxLines:]
		}
		for _, l := range lines {
			body = append(body, overlay.Style(bg).Foreground(pal.FgMute).Render("  "+bar)+
				overlay.Style(bg).Foreground(pal.FgDim).Render(overlay.Truncate(printableRunes(l), max(textW-2, 1))))
		}
		if len(pk.Options) > 0 {
			body = append(body, "")
			for _, o := range pk.Options {
				key := strconv.Itoa(o.N)
				body = append(body, overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("  "+key)+
					overlay.Style(bg).Foreground(pal.Fg).Render("  "+overlay.Truncate(printableTitle(o.Label), max(textW-len(key)-2, 1))))
			}
		}
		if !pk.Answerable {
			body = append(body, "")
			add(pal.FgDim, capitalize(printableTitle(pk.Reason))+". Enter goes to the pane.")
		}
		hints = m.inboxPeekHints(pk)
	}

	if p.Composing {
		body = append(body, "")
		add(pal.FgDim, "Answer:")
		for _, l := range p.Answer.Render(max(textW-2, 8), composerStyles(pal), bg) {
			body = append(body, overlay.Style(bg).Render("  ")+l)
		}
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	switch {
	case p.Sending:
		body = append(body, "")
		add(pal.FgDim, "Answering, and waiting for the pane to move on...")
	case p.Loading && pk != nil && overlay.ShowLoading(p.LoadingSince, now):
		body = append(body, "")
		add(pal.FgDim, "Reading the prompt again...")
	}
	if p.Err != "" {
		body = append(body, "")
		add(pal.Warn, p.Err)
	}
	if !p.Composing {
		hints = append(hints, m.keyHints(
			config.ActionPeekGo, "go to pane",
			config.ActionPeekReadAgain, "read again",
			config.ActionPeekBack, "back")...)
	}

	title := "Prompt"
	if pk != nil && pk.Kind != "" {
		title = inboxGroupTitle(inboxKindForPrompt(pk.Kind))
		title = strings.TrimSuffix(title, "s")
	}
	panel := overlay.Panel{
		Title: title + ": " + inboxWho(it),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// inboxKindForPrompt is the Inbox kind a prompt kind reads as.
func inboxKindForPrompt(kind string) string {
	if kind == harness.PromptKindQuestion || kind == harness.PromptKindAuth {
		return session.AttentionQuestion
	}
	return session.AttentionApproval
}

// sepWord is the separator between two clauses of a peek's first line.
func sepWord() string {
	if overlay.UseASCII() {
		return ", "
	}
	return " · "
}

// inboxPeekHints are the keys that answer a peeked prompt: only the answers
// the prompt takes now are offered, and one way to give each. A prompt with
// numbered options is answered by its digits, the numbers it shows; the
// approve, always and deny keys are offered only for a prompt with no
// options. It offered both, "1-3 choose" beside "a approve A always d deny",
// two ways to say one thing. Every key still works.
func (m *OS) inboxPeekHints(pk *session.PromptPeek) []overlay.Hint {
	var hints []overlay.Hint
	if pk.Offers(harness.ActionChoose) && len(pk.Options) > 0 {
		key := strconv.Itoa(pk.Options[0].N)
		if last := pk.Options[len(pk.Options)-1].N; last != pk.Options[0].N {
			key += "-" + strconv.Itoa(min(last, 9))
		}
		hints = append(hints, overlay.Hint{Key: key, Label: "choose"})
	} else {
		if pk.Offers(harness.ActionApprove) {
			hints = append(hints, m.keyHints(config.ActionPeekApprove, "approve")...)
		}
		if pk.Offers(harness.ActionApproveAlways) {
			hints = append(hints, m.keyHints(config.ActionPeekApproveAlways, "always")...)
		}
		if pk.Offers(harness.ActionDeny) {
			hints = append(hints, m.keyHints(config.ActionPeekDeny, "reject")...)
		}
	}
	if pk.Offers(harness.ActionText) {
		hints = append(hints, m.keyHints(config.ActionPeekType, "type")...)
	}
	return hints
}

// inboxAnswerOrder is each decision's key, in the order of the harness's own
// menu: yes, yes and do not ask again, no.
var inboxAnswerOrder = []struct{ key, decision, label string }{
	{"1", session.ApprovalOnce, "allow"},
	{"2", session.ApprovalAlways, "always"},
	{"3", session.ApprovalDeny, "reject"},
}

// inboxAnswerKeys is the keys that answer a held approval, such as "1/2/3",
// or empty for an item the Inbox is not holding.
func inboxAnswerKeys(it session.AttentionItem) string {
	if it.RequestID == "" {
		return ""
	}
	if it.Kind == session.AttentionAsk {
		return inboxAskKeys(it)
	}
	var keys []string
	for _, a := range inboxAnswerOrder {
		if slices.Contains(it.Options, a.decision) {
			keys = append(keys, a.key)
		}
	}
	return strings.Join(keys, "/")
}

// inboxApprovalDetail is the held approval under the cursor in full: its whole
// line, wrapped, and for always the rules it adds from now on. The daemon only
// holds a prompt whose line is the whole request, so this is everything the
// answer approves.
func inboxApprovalDetail(it session.AttentionItem, width int) []string {
	width = max(width-2, 8)
	if !inboxShowsWhole(it) {
		return wrapPlain("  This prompt has characters this terminal cannot show, so it is not answered here. Enter answers it in the pane.", width)
	}
	var lines []string
	for _, l := range wrapPlain(printableTitle(it.Summary), width) {
		lines = append(lines, "  "+l)
	}
	if slices.Contains(it.Options, session.ApprovalAlways) && len(it.AlwaysScope) > 0 {
		for _, l := range wrapPlain("2 (always) also allows from now on:", width) {
			lines = append(lines, "  "+l)
		}
		for _, rule := range it.AlwaysScope {
			for _, l := range wrapPlain(printableTitle(rule), width-2) {
				lines = append(lines, "    "+l)
			}
		}
	}
	return lines
}

// inboxShowsWhole reports whether this client draws a held approval's line
// and rules exactly as they are. A character it would leave out, such as one
// an ASCII-only terminal cannot draw, would make the line read as something
// it does not say, so such a prompt is answered in the pane.
func inboxShowsWhole(it session.AttentionItem) bool {
	lines := append([]string{it.Summary}, it.AlwaysScope...)
	if it.Kind == session.AttentionAsk {
		lines = append(lines, it.Options...)
	}
	for _, line := range lines {
		if printableTitle(line) != line || strings.IndexFunc(line, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return false
		}
	}
	return true
}

// inboxAskKeys is the digit keys that answer a question, such as "1-3".
func inboxAskKeys(it session.AttentionItem) string {
	switch n := min(len(it.Options), 9); n {
	case 0:
		return ""
	case 1:
		return "1"
	default:
		return "1-" + strconv.Itoa(n)
	}
}

// inboxAskDetail is the question under the cursor in full, wrapped, then each
// answer after the key that picks it.
func inboxAskDetail(it session.AttentionItem, width int) []string {
	width = max(width-2, 8)
	if !inboxShowsWhole(it) {
		return wrapPlain("  This question has characters this terminal cannot show, so it is not answered here.", width)
	}
	var lines []string
	for _, l := range wrapPlain(printableTitle(it.Summary), width) {
		lines = append(lines, "  "+l)
	}
	for i, o := range it.Options {
		if i >= 9 {
			break
		}
		for _, l := range wrapPlain(strconv.Itoa(i+1)+"  "+printableTitle(o), width-2) {
			lines = append(lines, "    "+l)
		}
	}
	return lines
}
