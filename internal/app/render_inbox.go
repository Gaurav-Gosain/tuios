package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const inboxWidth = 72

var defaultInboxKeys = sync.OnceValue(func() *config.KeybindRegistry {
	return config.NewKeybindRegistry(config.DefaultConfig())
})

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

func (m *OS) inboxKeyOr(action, fallback string) string {
	if k := m.inboxKey(action); k != "" {
		return k
	}
	return fallback
}

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

func (m *OS) keyHints(pairs ...string) []overlay.Hint {
	var hints []overlay.Hint
	for i := 0; i+1 < len(pairs); i += 2 {
		if key := m.inboxKey(pairs[i]); key != "" {
			h := overlay.Hint{Key: key, Label: pairs[i+1]}
			if pairs[i] == config.ActionInboxClose || pairs[i] == config.ActionInboxEscape {
				h.Priority = overlay.HintEssential
			}
			hints = append(hints, h)
		}
	}
	return hints
}

const inboxPeekMaxLines = 14

func (m *OS) renderInboxPeek(p *inboxPeek, now time.Time) (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
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
	add(pal.Fg, inboxWho(it)+" in "+m.inboxWhere(it)+" "+what+sepWord()+"waited "+units.Waited(since, now))
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
				overlay.Style(bg).Foreground(pal.FgDim).Render(overlay.Truncate(peekBodyLine(l), max(textW-2, 1))))
		}
		if pk.Offers(harness.ActionChoose) && len(pk.Options) > 0 {
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

func peekBodyLine(l string) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(l))
	for _, r := range l {
		switch {
		case printableRune(r, ascii):
			b.WriteRune(r)
		case strings.ContainsRune(harness.OptionCursors, r):
			if ascii {
				b.WriteByte('>')
			} else {
				b.WriteRune(r)
			}
		default:
			if w := ansi.StringWidth(string(r)); w > 0 {
				b.WriteString(strings.Repeat(" ", w))
			}
		}
	}
	return b.String()
}

func inboxKindForPrompt(kind string) string {
	if kind == harness.PromptKindQuestion || kind == harness.PromptKindAuth {
		return session.AttentionQuestion
	}
	return session.AttentionApproval
}

func sepWord() string {
	if overlay.UseASCII() {
		return ", "
	}
	return " · "
}

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

var inboxAnswerOrder = []struct{ key, decision, label string }{
	{"1", session.ApprovalOnce, "allow"},
	{"2", session.ApprovalAlways, "always"},
	{"3", session.ApprovalDeny, "reject"},
}

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
