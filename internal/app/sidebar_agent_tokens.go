package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type sidebarAgentToken struct {
	Name      string
	Text      string
	Number    float64
	HasNumber bool
}

func (m *OS) sidebarAgentTokenValue(name string, e sidebarAgentEntry, variant int, now time.Time) sidebarAgentToken {
	tk := sidebarAgentToken{Name: name}
	switch name {
	case "session":
		if e.Foreign {
			tk.Text = printableTitle(e.SessionLabel)
		}
	case "harness":
		h := sidebarHarnessLabel(e.Harness)
		if h == "" {
			if sum, ok := programSummary(e.Program); ok {
				h = printableTitle(sum.App)
			}
		}
		if !strings.EqualFold(h, sidebarAgentName(e)) {
			tk.Text = h
		}
	case "name":
		tk.Text = sidebarAgentName(e)
	case "state":
		tk.Text = sidebarStateWords(displayState(e.State, e.DoneSeen, e.Subagents))
	case "progress":
		if sum, ok := programSummary(e.Program); ok {
			if tk.Text = programProgressText(sum); tk.Text != "" {
				tk.Number, tk.HasNumber = float64(sum.Progress), true
			}
		}
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
		if e.State == "working" {
			tk.Text = printableTitle(toolLineLabel(sidebarAgentMetaValue(e.Meta, "now")))
		}
	case "prompt":
		tk.Text = printableTitle(sidebarAgentMetaValue(e.Meta, "prompt"))
	case "context":
		if pct, ok := sidebarContextPercent(sidebarAgentMetaValue(e.Meta, "context")); ok && pct >= config.SidebarContextWarnAt {
			tk.Text = "ctx " + strconv.Itoa(int(pct)) + "%"
			tk.Number, tk.HasNumber = pct, true
		}
	case "subagents":
		if e.ChildRows > 0 {
			tk.Text = subagentsRunText(runningSubagents(e.Children), len(e.Children))
		} else {
			tk.Text = session.SubagentsText(e.Subagents)
		}
		if tk.Text != "" {
			tk.Number, tk.HasNumber = float64(e.Subagents), true
		}
	case "pr":
		tk.Text = printableTitle(e.PR)
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

type sidebarAgentTokenPlan struct {
	Prefix []sidebarAgentToken
	Name   sidebarAgentToken
	After  []sidebarAgentToken
	Right  sidebarAgentToken
	Note   []sidebarAgentToken
}

func (m *OS) sidebarAgentTokensFor(e sidebarAgentEntry, variant int, tall bool, now time.Time) sidebarAgentTokenPlan {
	var plan sidebarAgentTokenPlan
	spec := &m.Settings.SidebarAgentRow
	beforeName := true
	needAt, messageAt := -1, -1
	for _, name := range spec.Tokens {
		if name == "meta" {
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
		if tk.Text == "" {
			continue
		}
		if beforeName {
			plan.Prefix = append(plan.Prefix, tk)
		} else {
			plan.After = append(plan.After, tk)
		}
	}
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

func sidebarTokenDefaultLook(tk sidebarAgentToken) config.SidebarTokenLook {
	switch tk.Name {
	case "context":
		return config.SidebarTokenLook{Fg: "warning"}
	case "pr":
		switch {
		case strings.HasSuffix(tk.Text, " "+ghpr.ChecksFail):
			return config.SidebarTokenLook{Fg: "error"}
		case strings.HasSuffix(tk.Text, " "+ghpr.ChecksPending):
			return config.SidebarTokenLook{Fg: "warning"}
		case strings.HasSuffix(tk.Text, " "+ghpr.ChecksPass), strings.HasSuffix(tk.Text, " "+ghpr.StateMerged):
			return config.SidebarTokenLook{Fg: "success"}
		}
	}
	return config.SidebarTokenLook{}
}

func sidebarQuietMark(avail, nameW int, sep string) string {
	word := sep + "quiet"
	if avail-lipgloss.Width(word) >= railNameKeep(nameW) {
		return word
	}
	if avail >= 3 {
		return " z"
	}
	return ""
}

func (m *OS) sidebarAgentNeedText(e sidebarAgentEntry, variant int, now time.Time) string {
	if e.Reason == session.AgentReasonSilent {
		return silentNote(e.StateAt, now)
	}
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

func silentNote(lastReport int64, now time.Time) string {
	d := now.Sub(time.Unix(0, lastReport))
	switch {
	case lastReport <= 0:
		return "No report"
	case d < time.Minute:
		return "No report for <1 min"
	case d < 2*time.Hour:
		return "No report for " + strconv.Itoa(int(d.Minutes())) + " min"
	}
	return "No report for " + strconv.Itoa(int(d.Hours())) + " h"
}

func sidebarNoteToken(name string) bool {
	switch name {
	case "message", "need", "now", "prompt", "context", "subagents", "pr":
		return true
	}
	_, ok := config.SidebarMetaTokenKey(name)
	return ok
}

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
	case "limit_reached":
		return sidebarStateWords(state), false
	case "done":
		if !doneSeen && message == "" {
			return sidebarStateWords(state), false
		}
	}
	return "", false
}

func sidebarPromptKind(kind string) bool {
	return kind == harness.PromptKindApproval || kind == harness.PromptKindQuestion || kind == harness.PromptKindAuth ||
		kind == inboxWordPlan || kind == inboxWordRisky
}

func sidebarAgentMessageRest(message string) string {
	_, rest, _ := strings.Cut(message, ": ")
	return strings.TrimSpace(rest)
}

func sidebarAgentMetaValue(meta []sessiontree.MetaToken, key string) string {
	for _, t := range meta {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

func (m *OS) sidebarAgentsHaveNotes(agents []sidebarAgentEntry, variant int) bool {
	now := time.Now()
	for _, e := range agents {
		if (!e.Head && e.sidebarSubRow() && m.Settings.SidebarSubagents.Fields.Note) || len(m.sidebarAgentTokensFor(e, variant, true, now).Note) > 0 {
			return true
		}
	}
	return false
}

func sidebarTokenColor(name string, pal overlay.Palette) color.Color {
	return theme.ConfigColorOn(name, &pal)
}

func (m *OS) sidebarTokenStyle(base lipgloss.Style, tk sidebarAgentToken, pal overlay.Palette) lipgloss.Style {
	look := sidebarTokenDefaultLook(tk).Overlay(m.Settings.SidebarAgentRow.Style(tk.Name).Resolve(tk.Text, tk.Number, tk.HasNumber))
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

func sidebarAgentSep() string {
	if overlay.UseASCII() {
		return " . "
	}
	return " · "
}

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

func (m *OS) sidebarAgentRowSpec() *config.SidebarAgentRowSpec { return &m.Settings.SidebarAgentRow }
