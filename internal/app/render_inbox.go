package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// inboxWidth is the Inbox overlay's preferred inner width: a row carries a
// name, a summary, the session and the wait.
const inboxWidth = 72

// inboxEmptyLines is the empty state: what the Inbox holds, so an empty one
// says what would appear here.
var inboxEmptyLines = []string{
	"Nothing is waiting for you.",
	"Agents that block on an approval or a question, write to you, error,",
	"finish a turn you have not looked at, or can resume after a restart",
	"show up here, from every session.",
}

// renderInbox renders the Inbox overlay: each kind under a heading in words,
// oldest first, with how long each item has waited. Nothing is said by colour
// alone: the heading names the kind, the row names the session and the wait.
func (m *OS) renderInbox() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.Inbox
	if st.Peek != nil {
		return m.renderInboxPeek(st.Peek, time.Now())
	}
	title := "Inbox"
	if st.Filter != "" {
		title += ": " + inboxGroupTitle(st.Filter)
	}
	if st.Select != "" {
		// In words in the title, so a narrowed Inbox never reads as an empty
		// one.
		title += " [select " + st.Select + "]"
	}
	if !st.Live {
		title += " (not connected)"
	}
	var detailFor func(int) []string
	if st.selectEditing {
		return m.renderInboxSelecting(title)
	}
	selected, ok := m.inboxSelected()
	hints := m.inboxRowHints(selected, ok)
	held := ok && inboxHeld(selected)
	if held {
		hints = m.inboxApprovalHints(selected)
		// The row cuts the line to fit, so the held prompt is shown whole
		// under the list, with what always adds beside its key. The keys
		// only answer the item under the cursor, which is the one shown.
		detailFor = func(width int) []string { return inboxApprovalDetail(selected, width) }
	}
	if ok && selected.Kind == session.AttentionAsk && selected.RequestID != "" {
		// A question is shown whole with its answers numbered, and the
		// digit keys pick one, under the same rule as a held approval.
		held = true
		hints = m.inboxAskHints(selected)
		detailFor = func(width int) []string { return inboxAskDetail(selected, width) }
	}
	if ok {
		// A plan, a risky approval or a finished turn's recap draws its own
		// detail and keys. See inbox_approvals_ext.go.
		if detail, extraHints, drawn := m.inboxDetailExtras(selected); drawn {
			detailFor, hints = detail, extraHints
		} else if detail, drawn := m.inboxRecapDetail(selected); drawn {
			detailFor = detail
		}
	}
	// The reply editor takes the detail and the footer while it is open,
	// whatever is under the cursor. See inbox_reply.go.
	replyDetail, replyHints, replying := m.inboxReplyDetail()
	if replying {
		detailFor, hints = replyDetail, replyHints
	}
	if m.InboxSnoozePicking() {
		// The next digit picks how long. The footer says the four lengths,
		// and nothing else answers until one is picked or the picker closes.
		hints = inboxSnoozeHints()
	}
	m.noteInboxShown(selected, held && inboxShowsWhole(selected), time.Now())
	rows := m.inboxRows()
	if len(rows) == 0 {
		lines := inboxEmptyLines
		switch {
		case st.Unsupported:
			lines = []string{"This daemon has no Inbox.", "Restart it with a newer tuios: tuios kill-server"}
		case st.Filter == session.AttentionMail:
			lines = []string{"No unread mail for you.", "m opens the mailbox, with every thread between agents too."}
		case st.Select != "" && len(st.Items) > 0:
			lines = []string{"Nothing matches select " + st.Select + ".", "/ changes the selector. Clear the line and press enter to show everything."}
		case st.Filter != "":
			lines = []string{"Nothing under " + inboxGroupTitle(st.Filter) + ".", "f shows the next kind, and then all of them."}
		case !m.IsDaemonSession:
			lines = []string{"The Inbox needs the daemon.", "", "Start a daemon session with: tuios new"}
		}
		if len(st.life.Snoozed) > 0 && !st.life.ShowSnoozed && st.Select == "" {
			lines = append(slices.Clone(lines), "", m.inboxSnoozedNote())
		}
		if replying {
			// A reply from the rail to a pane with nothing in the Inbox.
			return m.simpleOverlayPanel("", title, replyDetail(m.panelWidth(inboxWidth)), replyHints)
		}
		return m.simpleOverlayPanel("", title, lines, m.keyHints(
			config.ActionInboxFilter, "filter", config.ActionInboxSelect, "select",
			config.ActionInboxMailbox, "mailbox", config.ActionInboxClose, "close"))
	}
	now := time.Now()
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      inboxWidth,
		MaxVisible: 14,
		Count:      len(rows),
		Selected:   st.Selected,
		Scroll:     &st.Scroll,
		Hints:      hints,
		DetailFor:  detailFor,
		Position: func() (int, int) {
			n, of := 0, 0
			for i, r := range rows {
				if r.item == nil {
					continue
				}
				of++
				if i <= st.Selected {
					n = of
				}
			}
			return n, of
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			r := rows[i]
			if r.item == nil {
				return m.inboxHeadingRow(r, rowBg, pal, width)
			}
			return m.inboxItemRow(*r.item, selected, rowBg, pal, width, now)
		},
	})
}

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
			hints = append(hints, overlay.Hint{Key: key, Label: pairs[i+1]})
		}
	}
	return hints
}

// inboxRowHints is the Inbox footer for the row under the cursor: the key
// that answers it first, then going to it and dismissing it, then the keys
// that work on the list whatever is selected.
//
// It used to be the same eight hints on every row, over two lines, and said
// "space peek" where space does nothing (an error, a finished turn, mail) and
// "r reply" on rows that are not mail. A held approval and an ask-human
// question draw their own hints (inboxApprovalHints, inboxAskHints).
func (m *OS) inboxRowHints(it session.AttentionItem, ok bool) []overlay.Hint {
	var hints []overlay.Hint
	if ok {
		switch {
		case it.HeldID != 0:
			hints = m.keyHints(config.ActionInboxPassOn, "pass on")
		case it.Kind == session.AttentionMail:
			hints = m.keyHints(config.ActionInboxReply, "reply")
		case it.Kind == session.AttentionResume:
			hints = m.keyHints(config.ActionInboxResume, "resume")
		case inboxPeekable(it) && it.RequestID == "" && it.Host == "" && m.AttachedHost == "":
			// The one way to answer a prompt without leaving the Inbox: read it
			// here and press its answer.
			hints = m.keyHints(config.ActionInboxPeek, "answer")
		}
		goLabel := "go"
		if it.Kind == session.AttentionMail {
			goLabel = "open"
		}
		hints = append(hints, m.keyHints(config.ActionInboxGo, goLabel)...)
		// A finished or errored turn takes a reply, queued for when the
		// agent is at rest. See inbox_reply.go.
		if inboxReplyKind(it) && it.Host == "" && !it.Stale && m.inboxReplySupported() {
			hints = append(hints, m.keyHints(config.ActionInboxReply, "reply")...)
		}
		// Snooze is offered where it works, and a snoozed item offers to
		// wake on the same key.
		switch {
		case !m.inboxMarkSupported():
			// A daemon without mark-attention cannot snooze or wake.
		case it.SnoozedUntil != 0:
			hints = append(hints, m.keyHints(config.ActionInboxSnooze, "wake")...)
		case inboxSnoozeRefusal(it) == "":
			hints = append(hints, m.keyHints(config.ActionInboxSnooze, "snooze")...)
		}
		hints = append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss")...)
	}
	rowHints := len(hints)
	hints = append(hints, m.keyHints(config.ActionInboxFilter, "filter", config.ActionInboxSelect, "select")...)
	// The whole mailbox is one key from anywhere in the Inbox; it is offered
	// where it is the next thing a person looks for, on a mail row.
	if ok && it.Kind == session.AttentionMail {
		hints = append(hints, m.keyHints(config.ActionInboxMailbox, "mailbox")...)
		return append(hints, m.keyHints(config.ActionInboxClose, "close")...)
	}
	hints = append(hints, m.keyHints(config.ActionInboxClose, "close")...)
	// The keys for the row come first and the footer keeps to one line: the
	// keys that work on the whole list give way, the selector first, then the
	// filter. Both are in help, and the empty Inbox offers them.
	for len(hints)-1 > rowHints && overlay.HintRowCount(hints, inboxWidth) > 1 {
		hints = slices.Delete(hints, len(hints)-2, len(hints)-1)
	}
	return hints
}

// renderInboxSelecting draws the Inbox with the selector line open over the
// list. The list below it is narrowed by the selector in force, not by what is
// being typed, so what enter will do is shown only once it is done.
func (m *OS) renderInboxSelecting(title string) (string, overlay.Geometry, []overlayRowHit) {
	st := &m.Inbox
	rows := m.inboxRows()
	now := time.Now()
	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      inboxWidth,
		MaxVisible: 14,
		Search:     true,
		Query:      "select " + st.selectDraft,
		Count:      len(rows),
		Selected:   st.Selected,
		Scroll:     &st.Scroll,
		EmptyMsg:   "Nothing is shown under the selector in force.",
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "apply"},
			{Key: "esc", Label: "cancel"},
		},
		DetailFor: func(width int) []string {
			if st.selectErr != "" {
				return []string{overlay.Truncate("Not a selector: "+st.selectErr, width)}
			}
			return []string{overlay.Truncate("Terms: harness state needs:you session group host name, as key:value. Example: harness:codex needs:you", width)}
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			r := rows[i]
			if r.item == nil {
				return m.inboxHeadingRow(r, rowBg, pal, width)
			}
			return m.inboxItemRow(*r.item, selected, rowBg, pal, width, now)
		},
	})
}

// inboxHeadingRow draws a group heading: the kind in words and how many. A
// note row draws its words, muted.
func (m *OS) inboxHeadingRow(r inboxRow, bg color.Color, pal overlay.Palette, width int) string {
	if r.note != "" {
		return overlay.Style(bg).Foreground(pal.FgMute).Render(overlay.Truncate(r.note, width))
	}
	text := r.heading + " " + strconv.Itoa(r.count)
	return overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render(overlay.Truncate(text, width))
}

// inboxItemRow draws one item: its kind's mark, who, what it says, and on the
// right the session and how long it has waited.
func (m *OS) inboxItemRow(it session.AttentionItem, selected bool, bg color.Color, pal overlay.Palette, width int, now time.Time) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	// An item from a machine whose link is down is what that machine said
	// last. It says so in words, when it was last heard from, and is drawn in
	// the muted ink so the eye passes over it; the words carry it without
	// colour.
	when := inboxWait(it.Since, now)
	if it.Stale {
		when = inboxSeen(it.SeenAt, now)
	}
	// A snoozed item says when it wakes where an open one says how long it
	// has waited, and is drawn muted like a stale one.
	snoozed := it.SnoozedUntil != 0
	if snoozed {
		when = inboxSnoozedWhen(it.SnoozedUntil, now)
	}
	right := overlay.Style(bg).Foreground(pal.FgMute).Render(m.inboxWhere(it)+sep) +
		overlay.Style(bg).Foreground(pal.FgDim).Render(when)

	glyph := inboxKindGlyph(it.Kind) + " "
	who := inboxWho(it)
	if it.Count > 1 {
		who += " (" + strconv.Itoa(it.Count) + ")"
	}
	summary := printableTitle(it.Summary)
	if summary == "" {
		summary = inboxKindWords(it)
	}
	if extra := m.inboxRowExtras(it); extra != "" {
		// "risky: ", said in front of the line and after the keys.
		summary = extra + summary
	}
	if it.Kind == session.AttentionPlan {
		// A plan is its title and length; its keys are in the footer once
		// it is under the cursor.
		summary = inboxPlanRowText(it)
	} else if keys := inboxAnswerKeys(it); keys != "" && it.Kind == session.AttentionAsk {
		// A question's row says the range of its choices, which are its own
		// and not the Inbox's. An approval's keys are the footer's: once it
		// is under the cursor the footer names each one with what it does,
		// and the row saying "[1/3]" beside "1 allow 3 deny" said it twice.
		summary = "[" + keys + "] " + summary
	}
	if it.HeldID != 0 {
		// Mail another machine sent an agent here, held for the person by
		// the link policy. Said in words: who it was for, and the key.
		summary = "[held for " + printableTitle(it.HeldFor) + ", p passes on] " + summary
	}

	avail := max(width-lipgloss.Width(right)-lipgloss.Width(glyph)-4, 1)
	whoW := min(lipgloss.Width(who), avail)
	summaryW := max(avail-whoW-2, 0)

	whoColor := pal.FgDim
	if selected {
		whoColor = pal.Fg
	}
	glyphColor := inboxKindColor(it.Kind, pal)
	if it.Stale || snoozed {
		whoColor, glyphColor = pal.FgMute, pal.FgMute
	}
	left := overlay.Style(bg).Foreground(glyphColor).Render(glyph) +
		overlay.Style(bg).Foreground(whoColor).Bold(true).Render(overlay.Truncate(who, whoW))
	if summaryW >= 2 {
		left += overlay.Style(bg).Foreground(pal.FgDim).Render("  " + overlay.Truncate(summary, summaryW))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, bg, pal)
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
		body = append(body, "")
		add(pal.FgDim, "Reading the prompt...")
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
		add(pal.Fg, "Answer: "+printableRunes(p.Draft)+"_")
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	switch {
	case p.Sending:
		body = append(body, "")
		add(pal.FgDim, "Answering, and waiting for the pane to move on...")
	case p.Loading && pk != nil:
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
		Glyph: inboxKindGlyph(it.Kind),
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
	if kind == harness.PromptKindQuestion {
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
			hints = append(hints, m.keyHints(config.ActionPeekDeny, "deny")...)
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
	{"3", session.ApprovalDeny, "deny"},
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

// inboxApprovalHints are the hints for a held approval under the cursor: its
// answers first, then going to the pane, which hands the prompt back there.
func (m *OS) inboxApprovalHints(it session.AttentionItem) []overlay.Hint {
	var hints []overlay.Hint
	for _, a := range inboxAnswerOrder {
		if slices.Contains(it.Options, a.decision) {
			hints = append(hints, overlay.Hint{Key: a.key, Label: a.label})
		}
	}
	return append(hints, m.keyHints(
		config.ActionInboxGo, "answer in pane",
		config.ActionInboxDismiss, "dismiss",
		config.ActionInboxClose, "close")...)
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

// inboxAskHints are the hints for a question under the cursor: the digits
// that pick an answer, then going to the pane that asked.
func (m *OS) inboxAskHints(it session.AttentionItem) []overlay.Hint {
	hints := []overlay.Hint{{Key: inboxAskKeys(it), Label: "answer"}}
	if it.Window != "" {
		hints = append(hints, m.keyHints(config.ActionInboxGo, "go to pane")...)
	}
	return append(hints, m.keyHints(config.ActionInboxDismiss, "dismiss", config.ActionInboxClose, "close")...)
}

// inboxKindColor is the ink of a kind's mark, the same the rail gives the
// state behind it.
func inboxKindColor(kind string, pal overlay.Palette) color.Color {
	switch kind {
	case session.AttentionMail, session.AttentionResume:
		return pal.AccentBright
	case session.AttentionFinished:
		return pal.Success
	}
	return sidebarSeverityColor(inboxAlertState(kind), pal)
}
