package app

import (
	"image/color"
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
		tk.Text = strings.ReplaceAll(e.State, "_", " ")
	case "elapsed":
		if variant == sidebarVariantFull {
			tk.Text = agentElapsed(e.State, e.StateAt, now)
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
					if spec.Has("$" + t.Key) {
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
		if _, kind := sidebarAgentNeed(e.State, e.DoneSeen, e.Message); kind {
			rest := sidebarAgentMessageRest(plan.Note[messageAt].Text)
			if rest == "" {
				plan.Note = append(plan.Note[:messageAt], plan.Note[messageAt+1:]...)
			} else {
				plan.Note[messageAt].Text = rest
			}
		}
	}
	return plan
}

// sidebarAgentNeedText is the need token as drawn: the word, and the wait when
// the identity line is not showing it, which is the narrow rail and a row with
// no elapsed token. How long a pane has been waiting on you is the one figure
// a row that needs you must not lose.
func (m *OS) sidebarAgentNeedText(e sidebarAgentEntry, variant int, now time.Time) string {
	word, _ := sidebarAgentNeed(e.State, e.DoneSeen, e.Message)
	if sidebarAgentGroup(e.State, e.DoneSeen) != sidebarGroupNeedsYou {
		return word
	}
	if variant == sidebarVariantFull && m.Settings.SidebarAgentRow.Has("elapsed") {
		return word
	}
	wait := agentElapsed(e.State, e.StateAt, now)
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
	if name == "message" || name == "need" {
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
// The word gives way to a message that says it better. "approval" or
// "question" is lifted off the front of the message, so it costs nothing. A
// row that needs you and reported its own message ("awaiting approval") keeps
// the message and gets no word: on a 28-column rail the two would not both
// fit, and the message is the one that says what to do.
func sidebarAgentNeed(state string, doneSeen bool, message string) (string, bool) {
	switch state {
	case "needs_input":
		if kind, _, ok := strings.Cut(message, ": "); ok && (kind == harness.PromptKindApproval || kind == harness.PromptKindQuestion) {
			return kind, true
		}
		if message == "" {
			return "needs input", false
		}
	case "errored":
		if message == "" {
			return "errored", false
		}
	case "done":
		if !doneSeen && message == "" {
			return "finished", false
		}
	}
	return "", false
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
	look := m.Settings.SidebarAgentRow.Style(tk.Name).Resolve(tk.Text, tk.Number, tk.HasNumber)
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

// sidebarAgentRun draws a run of tokens joined by sep, dropping tokens from
// the end until the run fits in avail cells, and reports what it took. A run
// that fits nothing draws nothing. Each token is styled on its own, so a rule
// on one of them cannot ink its neighbour.
func (m *OS) sidebarAgentRun(tokens []sidebarAgentToken, sep string, baseFor func(sidebarAgentToken) lipgloss.Style, sepStyle lipgloss.Style, avail int, pal overlay.Palette) (string, int) {
	for len(tokens) > 0 {
		w := 0
		for i, tk := range tokens {
			if i > 0 {
				w += lipgloss.Width(sep)
			}
			w += lipgloss.Width(tk.Text)
		}
		if w <= avail {
			var b strings.Builder
			for i, tk := range tokens {
				if i > 0 {
					b.WriteString(sepStyle.Render(sep))
				}
				b.WriteString(m.sidebarTokenStyle(baseFor(tk), tk, pal).Render(tk.Text))
			}
			return b.String(), w
		}
		tokens = tokens[:len(tokens)-1]
	}
	return "", 0
}

// sidebarAgentPrefixRun is the prefix in front of the name: the tokens joined
// with "/" and a trailing "/", giving way from the front, whole tokens at a
// time, before a cell of the name goes. The session goes first because the
// row's gutter already carries a tint for a pane that is somewhere else, while
// nothing else on the row says which agent it is.
func (m *OS) sidebarAgentPrefixRun(tokens []sidebarAgentToken, base lipgloss.Style, avail int, pal overlay.Palette) (string, int) {
	for len(tokens) > 0 {
		w := 0
		for _, tk := range tokens {
			w += lipgloss.Width(tk.Text) + 1
		}
		// Two cells beyond the prefix, so the name it fronts keeps something.
		if w+2 <= avail {
			var b strings.Builder
			for _, tk := range tokens {
				b.WriteString(m.sidebarTokenStyle(base, tk, pal).Render(tk.Text))
				b.WriteString(base.Render("/"))
			}
			return b.String(), w
		}
		tokens = tokens[1:]
	}
	return "", 0
}

// sidebarAgentRowSpec is the spec in force, for callers outside the render.
func (m *OS) sidebarAgentRowSpec() *config.SidebarAgentRowSpec { return &m.Settings.SidebarAgentRow }
