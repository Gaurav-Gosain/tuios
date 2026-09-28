package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// An agent row is drawn from tokens: the facts the row has about its pane, each
// with a name a person can write in config. Which tokens the row shows and in
// what order is [appearance.sidebar.agent_row].tokens; how each is inked is the
// token's own table and its value rules. See config/sidebar_agent_row.go for
// the surface and its reasons.
//
// The row keeps its shape whatever the order says. Tokens before name are the
// prefix, joined with "/" as the session and harness always were; tokens after
// it follow the name with the rail's separator; elapsed sits at the right
// edge; message takes the second line when the rail has room for one, and
// harness moves down to that line with it, which is where the two were before
// any of this was configurable. So the shipped order draws the shipped row,
// and a person who reorders the list moves things within those homes.

// sidebarAgentToken is one token of one row: its drawn text, and its number
// when the token is one, which is what the gt and lt rules read.
type sidebarAgentToken struct {
	Name      string
	Text      string
	Number    float64
	HasNumber bool
}

// sidebarAgentTokenValue resolves one token for an entry. variant says whether
// the rail is wide enough for the elapsed figure, which the narrow rail drops.
func (m *OS) sidebarAgentTokenValue(name string, e sidebarAgentEntry, variant int, now time.Time) sidebarAgentToken {
	tk := sidebarAgentToken{Name: name}
	switch name {
	case "session":
		if e.Foreign {
			tk.Text = printableTitle(e.SessionLabel)
		}
	case "harness":
		// A pane running an agent is usually already named after it, and a
		// row reading "claude/claude" spends half its width saying one thing
		// twice. The token earns its cells only when it adds a name.
		if h := sidebarHarnessLabel(e.Harness); !strings.EqualFold(h, sidebarAgentName(e)) {
			tk.Text = h
		}
	case "name":
		tk.Text = sidebarAgentName(e)
	case "state":
		tk.Text = sidebarStateWords(e.State)
	case "elapsed":
		if variant == sidebarVariantFull {
			tk.Text = railAgentAge(e.State, e.StateAt, now)
			if tk.Text != "" {
				tk.Number = now.Sub(time.Unix(0, e.StateAt)).Minutes()
				tk.HasNumber = true
			}
		}
	case "message":
		tk.Text = printableTitle(e.Message)
	case "need":
		tk.Text = m.sidebarAgentNeedText(e, variant, now)
	case "host":
		tk.Text = printableTitle(e.Host)
	case "now":
		// What a working agent is doing. The daemon clears it at rest, and a
		// pane blocked on a prompt says what it asks in its need and message,
		// so it draws only while the agent works.
		if e.State == "working" {
			tk.Text = printableTitle(sidebarAgentMetaValue(e.Meta, "now"))
		}
	case "prompt":
		tk.Text = printableTitle(sidebarAgentMetaValue(e.Meta, "prompt"))
	case "context":
		if pct, ok := sidebarContextPercent(sidebarAgentMetaValue(e.Meta, "context")); ok && pct >= config.SidebarContextWarnAt {
			tk.Text = "ctx " + strconv.Itoa(int(pct)) + "%"
			tk.Number, tk.HasNumber = pct, true
		}
	default:
		if key, ok := config.SidebarMetaTokenKey(name); ok {
			tk.Text = printableTitle(sidebarAgentMetaValue(e.Meta, key))
		}
	}
	if !tk.HasNumber && tk.Text != "" {
		if f, err := strconv.ParseFloat(tk.Text, 64); err == nil {
			tk.Number, tk.HasNumber = f, true
		}
	}
	return tk
}

// sidebarAgentTokenPlan is where each configured token lands on a row.
type sidebarAgentTokenPlan struct {
	// Prefix is the run before the name, joined with "/".
	Prefix []sidebarAgentToken
	// Name is the name token, or empty text when the list leaves it out.
	Name sidebarAgentToken
	// After is the run following the name, joined with the rail's separator.
	After []sidebarAgentToken
	// Right is the figure at the right edge: elapsed, when it is listed.
	Right sidebarAgentToken
	// Note is the second line, in list order.
	Note []sidebarAgentToken
}

// sidebarAgentTokensFor places an entry's tokens. tall says the row has a
// second line, which is where message goes and harness goes with it.
func (m *OS) sidebarAgentTokensFor(e sidebarAgentEntry, variant int, tall bool, now time.Time) sidebarAgentTokenPlan {
	var plan sidebarAgentTokenPlan
	spec := &m.Settings.SidebarAgentRow
	beforeName := true
	needAt, messageAt := -1, -1
	for _, name := range spec.Tokens {
		if name == "meta" {
			// Every key the pane reported that no $key token places itself,
			// in the pane's own order.
			if tall {
				for _, t := range e.Meta {
					if spec.Has("$"+t.Key) || slices.Contains(config.SidebarFeedMetaKeys, t.Key) {
						continue
					}
					tk := sidebarAgentToken{Name: "$" + t.Key, Text: printableTitle(t.Value)}
					if tk.Text == "" {
						continue
					}
					if f, err := strconv.ParseFloat(tk.Text, 64); err == nil {
						tk.Number, tk.HasNumber = f, true
					}
					plan.Note = append(plan.Note, tk)
				}
			}
			continue
		}
		tk := m.sidebarAgentTokenValue(name, e, variant, now)
		switch {
		case name == "name":
			plan.Name = tk
			beforeName = false
			continue
		case name == "elapsed":
			plan.Right = tk
			continue
		case sidebarNoteToken(name) || (name == "harness" && tall):
			if tall && tk.Text != "" {
				switch name {
				case "need":
					needAt = len(plan.Note)
				case "message":
					messageAt = len(plan.Note)
				}
				plan.Note = append(plan.Note, tk)
			}
			continue
		}
		// A missing value and its separator vanish.
		if tk.Text == "" {
			continue
		}
		if beforeName {
			plan.Prefix = append(plan.Prefix, tk)
		} else {
			plan.After = append(plan.After, tk)
		}
	}
	// A screen rule's message is "approval: <the prompt>", and the need token
	// already said approval, so the message keeps only the prompt.
	if needAt >= 0 && messageAt >= 0 {
		if _, kind := sidebarAgentNeed(e.State, e.DoneSeen, e.AgentKind, e.Message); kind {
			rest := sidebarAgentMessageRest(plan.Note[messageAt].Text)
			if rest == "" {
				plan.Note = append(plan.Note[:messageAt], plan.Note[messageAt+1:]...)
			} else {
				plan.Note[messageAt].Text = rest
			}
		}
	}
	plan.Note = sidebarAgentQuietMessage(plan.Note, e)
	return plan
}

// sidebarAgentQuietMessage drops the message from a note line where it says
// less than the row's own rule: on a working row that shows what the agent is
// doing now, which is the same fact fresher, and on a row at rest, which
// needs nothing from anyone and whose last note is old news. A finished turn
// not yet seen keeps its message, which is the first line of what the agent
// said, and a row that needs you keeps what it asks.
func sidebarAgentQuietMessage(note []sidebarAgentToken, e sidebarAgentEntry) []sidebarAgentToken {
	drop := false
	switch sidebarAgentGroup(e.State, e.DoneSeen) {
	case sidebarGroupWorking:
		drop = slices.ContainsFunc(note, func(tk sidebarAgentToken) bool { return tk.Name == "now" && tk.Text != "" })
	case sidebarGroupIdle:
		drop = true
	}
	if !drop {
		return note
	}
	return slices.DeleteFunc(note, func(tk sidebarAgentToken) bool { return tk.Name == "message" })
}

// sidebarContextPercent reads a context value as a percent: "42%", "42.5%" or
// "42". ok is false for anything else, such as a token count a harness sent
// on its own, which says nothing about how full the window is.
func sidebarContextPercent(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	num, _, _ := strings.Cut(v, "%")
	num = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(num), " ctx"))
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 || f > 100 {
		return 0, false
	}
	return f, true
}

// sidebarTokenDefaultLook is the look a token has before its table says
// anything: the context warning is in the warning ink, and every other token
// starts from the rail's own style.
func sidebarTokenDefaultLook(name string) config.SidebarTokenLook {
	if name == "context" {
		return config.SidebarTokenLook{Fg: "warning"}
	}
	return config.SidebarTokenLook{}
}

// sidebarAgentNeedText is the need token as drawn: the word, and the wait when
// the identity line is not showing it, which is the narrow rail and a row with
// no elapsed token. How long a pane has been waiting on you is the one figure
// a row that needs you must not lose.
func (m *OS) sidebarAgentNeedText(e sidebarAgentEntry, variant int, now time.Time) string {
	word, _ := sidebarAgentNeed(e.State, e.DoneSeen, e.AgentKind, e.Message)
	if sidebarAgentGroup(e.State, e.DoneSeen) != sidebarGroupNeedsYou {
		return word
	}
	if variant == sidebarVariantFull && m.Settings.SidebarAgentRow.Has("elapsed") {
		return word
	}
	wait := railAgentAge(e.State, e.StateAt, now)
	switch {
	case wait == "":
		return word
	case word == "":
		return "waiting " + wait
	default:
		return word + " " + wait
	}
}

// sidebarNoteToken reports the tokens that only ever draw on a row's second
// line: the note the pane reported, what the row needs from you, and the
// pane's metadata. None of them has room on the identity line, which is the
// name's.
func sidebarNoteToken(name string) bool {
	switch name {
	case "message", "need", "now", "prompt", "context":
		return true
	}
	_, ok := config.SidebarMetaTokenKey(name)
	return ok
}

// sidebarAgentNeed is what a row wants from the person, in a word, and whether
// the word came from the kind a screen rule put in front of the message. It is
// the text half of the state: the glyph and its colour say the same thing, and
// the word is what still says it on a rail drawn without colour or glyphs.
// Working and resting rows need nothing and get no word.
//
// kind is the block the daemon recorded for a needs_input pane (approval or
// question), empty when the source did not say.
//
// The word is said once. "approval" or "question" in front of a screen rule's
// message is lifted off it, so it costs nothing. A hook's message ("approve
// Bash: make") does not say its kind, so the recorded kind is the word and the
// message stays whole. A message that already names its kind ("awaiting
// approval") gets no word, and neither does one of no known kind: the message
// is the one that says what to do.
func sidebarAgentNeed(state string, doneSeen bool, kind, message string) (string, bool) {
	switch state {
	case "needs_input":
		if lead, _, ok := strings.Cut(message, ": "); ok && sidebarPromptKind(lead) {
			return lead, true
		}
		if sidebarPromptKind(kind) && !strings.Contains(strings.ToLower(message), kind) {
			return kind, false
		}
		if message == "" {
			return sidebarStateWords(state), false
		}
	case "errored":
		if message == "" {
			return "errored", false
		}
	case "done":
		if !doneSeen && message == "" {
			return sidebarStateWords(state), false
		}
	}
	return "", false
}

// sidebarPromptKind reports whether kind is one of the prompt kinds a need
// word can name.
func sidebarPromptKind(kind string) bool {
	return kind == harness.PromptKindApproval || kind == harness.PromptKindQuestion ||
		kind == inboxWordPlan || kind == inboxWordRisky
}

// sidebarAgentMessageRest is a message with the prompt kind in front of it
// taken off: "approval: run tests?" is "run tests?".
func sidebarAgentMessageRest(message string) string {
	_, rest, _ := strings.Cut(message, ": ")
	return strings.TrimSpace(rest)
}

// sidebarAgentMetaValue is the value of one metadata key, empty when the pane
// did not report it.
func sidebarAgentMetaValue(meta []sessiontree.MetaToken, key string) string {
	for _, t := range meta {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

// sidebarAgentsHaveNotes reports whether any of these agents has something to
// put on a second line. A section where none of them does would pay two lines a
// row for a column of blanks.
func (m *OS) sidebarAgentsHaveNotes(agents []sidebarAgentEntry, variant int) bool {
	now := time.Now()
	for _, e := range agents {
		if len(m.sidebarAgentTokensFor(e, variant, true, now).Note) > 0 {
			return true
		}
	}
	return false
}

// sidebarTokenColor is the colour a configured name means, on this palette. A
// hex literal is itself; a name the palette does not know is nil, which the
// caller reads as "leave the rail's own colour".
func sidebarTokenColor(name string, pal overlay.Palette) color.Color {
	switch name {
	case "text":
		return pal.Fg
	case "dim":
		return pal.FgDim
	case "muted":
		return pal.FgMute
	case "accent":
		return pal.Accent
	case "warning":
		return pal.Warning
	case "error":
		return pal.Warn
	case "success":
		return pal.Success
	case "info":
		return pal.Info
	}
	if c, ok := parseHexColor(name); ok {
		return c
	}
	return nil
}

// sidebarTokenStyle is the rail's own style for a token with the configured
// look written over it: fg replaces the colour, bold and dim replace the
// rail's choice when they are set at all.
func (m *OS) sidebarTokenStyle(base lipgloss.Style, tk sidebarAgentToken, pal overlay.Palette) lipgloss.Style {
	look := sidebarTokenDefaultLook(tk.Name).Overlay(m.Settings.SidebarAgentRow.Style(tk.Name).Resolve(tk.Text, tk.Number, tk.HasNumber))
	if c := sidebarTokenColor(look.Fg, pal); c != nil {
		base = base.Foreground(c)
	}
	if look.Bold != nil {
		base = base.Bold(*look.Bold)
	}
	if look.Dim != nil {
		base = base.Faint(*look.Dim)
	}
	return base
}

// sidebarAgentSep is the separator between tokens that follow the name and
// between the parts of the note line.
func sidebarAgentSep() string {
	if overlay.UseASCII() {
		return " . "
	}
	return " · "
}

// sidebarAgentBudget is what each token of an agent row's identity line
// costs, in the order the row gives them way, for railRowFit: the prefix, the
// tokens after the name, then the figure at the right edge and the mail count
// beside it. A prefix token costs its text and the "/" after it, and is kept
// only while the whole name fits beside it. A token after the name costs its
// text and the separator in front of it.
//
// It appends to dst, which may be nil, and returns the list.
func sidebarAgentBudget(dst []railToken, prefix, after []sidebarAgentToken, label, mail, sep string) []railToken {
	tokens := dst[:0]
	for _, tk := range prefix {
		tokens = append(tokens, railToken{Cost: lipgloss.Width(tk.Text) + 1, Whole: true})
	}
	for _, tk := range after {
		tokens = append(tokens, railToken{Cost: lipgloss.Width(sep) + lipgloss.Width(tk.Text)})
	}
	return append(tokens,
		railToken{Cost: sidebarFigureCost(label), Right: true},
		railToken{Cost: sidebarFigureCost(mail), Right: true})
}

// sidebarAgentFit lays out an agent row's identity line: the name, the tokens
// around it, the figure at the right edge and the mail count, in avail cells.
// label is the elapsed time, and queued the forms of the queued-message figure
// that takes its place when messages wait, longest first.
//
// The queued figure is picked inside the budget. The longest form that keeps
// every token the row would draw without it wins, so a row reads
// "deploy-the-api-… 2q · working" rather than dropping the state to spell out
// "2 queued". When every form costs a token, the longest form the budget
// keeps at all wins, and when none is kept the elapsed time stays.
//
// It reports the figure it drew, which tokens the row keeps in
// sidebarAgentBudget's order, and the cells the name may take. The answer is
// written into s, so it holds until the next row is fitted.
func sidebarAgentFit(s *railScratch, prefix, after []sidebarAgentToken, nameW int, label string, queued []string, mail, sep string, avail int) (figure string, keep []bool, nameRoom int) {
	fit := func(figure string) ([]bool, int) {
		s.tokens = sidebarAgentBudget(s.tokens, prefix, after, figure, mail, sep)
		s.keep, nameRoom = railRowFitInto(s.keep, nameW, railNameKeep(nameW), s.tokens, avail)
		return s.keep, nameRoom
	}
	labelAt := len(prefix) + len(after)
	figure = label
	kept := ""
	for _, q := range queued {
		keep, _ := fit(q)
		if !keep[labelAt] {
			continue
		}
		if kept == "" {
			kept = q
		}
		all := true
		for i, tk := range s.tokens {
			if tk.Cost > 0 && !tk.Whole && !keep[i] {
				all = false
				break
			}
		}
		if all {
			kept = q
			break
		}
	}
	if kept != "" {
		figure = kept
	}
	keep, nameRoom = fit(figure)
	return figure, keep, nameRoom
}

// sidebarAgentRun draws the tokens the row's budget kept after the name, each
// led by sep. See railRowFit. Each token is styled on its own, so a rule on
// one of them cannot ink its neighbour, and each is drawn whole, so a rule
// always inks the value the person reads.
func (m *OS) sidebarAgentRun(tokens []sidebarAgentToken, keep []bool, sep string, baseFor func(sidebarAgentToken) lipgloss.Style, sepStyle lipgloss.Style, pal overlay.Palette) string {
	var b strings.Builder
	for i, tk := range tokens {
		if !keep[i] {
			continue
		}
		b.WriteString(sepStyle.Render(sep))
		b.WriteString(m.sidebarTokenStyle(baseFor(tk), tk, pal).Render(tk.Text))
	}
	return b.String()
}

// sidebarAgentPrefixRun draws the prefix in front of the name: the tokens the
// row's budget kept, each with the "/" that joins it to what follows. The
// budget keeps a prefix token only while the whole name fits beside it, so on
// a narrow rail the row reads "deploy" rather than "claude/depl…".
func (m *OS) sidebarAgentPrefixRun(tokens []sidebarAgentToken, keep []bool, base lipgloss.Style, pal overlay.Palette) string {
	var b strings.Builder
	for i, tk := range tokens {
		if !keep[i] {
			continue
		}
		b.WriteString(m.sidebarTokenStyle(base, tk, pal).Render(tk.Text))
		b.WriteString(base.Render("/"))
	}
	return b.String()
}

// sidebarAgentRowSpec is the spec in force, for callers outside the render.
func (m *OS) sidebarAgentRowSpec() *config.SidebarAgentRowSpec { return &m.Settings.SidebarAgentRow }
