package vis

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func renderRail(s Snapshot, o Options, pal overlay.Palette, w, h int) []string {
	bg := pal.Panel
	var out []string
	add := func(parts ...string) { out = append(out, row(w, bg, parts...)) }
	blank := func() { add("") }
	rule := func() { add(overlay.Rule(w-4, bg, pal), "") }

	g := glyphs()
	label := ink(bg, pal.FgMute)
	value := ink(bg, pal.FgDim)
	pad := ink(bg, bg).Render("  ")

	status, statusInk := "generating", ink(bg, pal.FgDim)
	switch {
	case s.Violated && s.Phase == PhaseShrinking:
		status, statusInk = "shrinking", bold(bg, pal.Warning)
	case s.Violated:
		status, statusInk = "falsified", bold(bg, pal.Warn)
	case s.Phase == PhaseDone:
		status, statusInk = "held", bold(bg, pal.Success)
	}
	add(pad, overlay.Chip("tuios fuzz", pal.Accent, pal.PillFg), ink(bg, bg).Render(" "), statusInk.Render(status))
	blank()

	add(pad, label.Render("seed    "), value.Render(hex16(s.Seed)))
	add(pad, label.Render("actions "), value.Render(commas(s.Actions)), label.Render(" of "), value.Render(commas(s.Steps)))
	add(pad, label.Render("elapsed "), value.Render(duration(s.Elapsed)), label.Render("  "+g.sep+" "), value.Render(rate(s.Rate, s.Elapsed, "/s")))
	blank()
	rule()

	out = append(out, renderInvariants(s, pal, w)...)
	rule()
	out = append(out, renderMix(s, pal, w)...)
	rule()
	out = append(out, renderLedger(s, pal, w)...)

	body := fit(out, w, max(h-1, 0), bg)
	hints := []overlay.Hint{{Key: "q", Label: "quit"}}
	if s.Done {
		hints = append([]overlay.Hint{{Key: "r", Label: "run again"}}, hints...)
	}
	strip := []string{row(w, bg, pad, hintStrip(hints, bg, pal))}
	return append(body, fit(strip, w, 1, bg)...)
}

func hintStrip(hints []overlay.Hint, bg color.Color, pal overlay.Palette) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts,
			overlay.KeyBadge(h.Key, pal)+ink(bg, pal.FgMute).Render(" "+h.Label))
	}
	return strings.Join(parts, ink(bg, bg).Render("  "))
}

func renderInvariants(s Snapshot, pal overlay.Palette, w int) []string {
	bg := pal.Panel
	g := glyphs()
	var out []string
	add := func(parts ...string) { out = append(out, row(w, bg, parts...)) }
	pad := ink(bg, bg).Render("  ")
	label := ink(bg, pal.FgMute)

	broken := 0
	for _, b := range s.Broken {
		if b {
			broken++
		}
	}

	verdict, verdictInk := itoa(len(s.Rules))+" ok", ink(bg, pal.FgDim)
	if broken > 0 {
		verdict, verdictInk = itoa(broken)+" failed", bold(bg, pal.Warn)
	}
	head := label.Render("invariants")
	gap := max(w-4-lipgloss.Width("invariants")-lipgloss.Width(verdict), 1)
	add(pad, head, ink(bg, bg).Render(strings.Repeat(" ", gap)), verdictInk.Render(verdict))

	for _, fam := range families(s.Rules) {
		marks := make([]string, 0, len(fam.idx))
		for _, i := range fam.idx {
			if i < len(s.Broken) && s.Broken[i] {
				marks = append(marks, bold(bg, pal.Warn).Render(g.fail))
				continue
			}
			marks = append(marks, ink(bg, pal.FgDim).Render(g.dot))
		}
		name := overlay.Truncate(fam.name, 10)
		add(pad, label.Render(name+strings.Repeat(" ", max(11-lipgloss.Width(name), 1))),
			strings.Join(marks, ink(bg, bg).Render(" ")))
	}

	failWord, failInk := "0 failed", ink(bg, pal.FgDim)
	if broken > 0 {
		failWord, failInk = itoa(broken)+" failed", bold(bg, pal.Warn)
	}
	add(pad, label.Render("checks "), ink(bg, pal.FgDim).Render(commas(s.Checks)),
		label.Render(" "+g.sep+" "), failInk.Render(failWord))

	if s.Violated {
		out = append(out, renderCallout(s, pal, w)...)
	}
	return out
}

func renderCallout(s Snapshot, pal overlay.Palette, w int) []string {
	bg := pal.Panel
	g := glyphs()
	var out []string
	out = append(out, row(w, bg, ink(bg, bg).Render("  "),
		bold(bg, pal.Warn).Render(g.fail+" "+overlay.Truncate(s.FailedRule.Name, w-6))))
	if doc := s.FailedRule.Doc; doc != "" {
		for _, l := range wrap(doc, w-6) {
			out = append(out, row(w, bg, ink(bg, bg).Render("    "), ink(bg, pal.FgDim).Render(l)))
		}
	}
	if s.FailedStep >= 0 {
		out = append(out, row(w, bg, ink(bg, bg).Render("    "),
			ink(bg, pal.FgMute).Render("at action "+commas(s.FailedStep))))
	}
	return out
}

type family struct {
	name string
	idx  []int
}

func families(rules []fuzz.RuleInfo) []family {
	var out []family
	at := map[string]int{}
	for i, r := range rules {
		j, ok := at[r.Family]
		if !ok {
			at[r.Family] = len(out)
			out = append(out, family{name: r.Family})
			j = len(out) - 1
		}
		out[j].idx = append(out[j].idx, i)
	}
	return out
}

func renderMix(s Snapshot, pal overlay.Palette, w int) []string {
	bg := pal.Panel
	g := glyphs()
	var out []string
	add := func(parts ...string) { out = append(out, row(w, bg, parts...)) }
	pad := ink(bg, bg).Render("  ")
	label := ink(bg, pal.FgMute)
	add(pad, label.Render("mix"))

	peak := 0
	for _, n := range s.Mix {
		peak = max(peak, n)
	}
	const barCells = 14
	for i, c := range s.Classes {
		n := 0
		if i < len(s.Mix) {
			n = s.Mix[i]
		}
		cells := 0
		if peak > 0 {
			cells = n * barCells / peak
		}
		bar := strings.Repeat(g.block, cells) + strings.Repeat(" ", barCells-cells)
		figure := pal.FgDim
		if s.Violated {
			figure = pal.FgMute
		}
		add(pad,
			ink(bg, figure).Render(c.Letter),
			label.Render(" "+c.Name+strings.Repeat(" ", max(9-len(c.Name), 1))),
			ink(bg, pal.FgMute).Render(bar),
			ink(bg, figure).Render(" "+commas(n)))
	}
	return out
}

func renderLedger(s Snapshot, pal overlay.Palette, w int) []string {
	bg := pal.Panel
	var out []string
	add := func(parts ...string) { out = append(out, row(w, bg, parts...)) }
	pad := ink(bg, bg).Render("  ")
	add(pad, ink(bg, pal.FgMute).Render("last"))
	for i := range s.LedgerN {
		fg := pal.FgDim
		if i == 0 {
			fg = pal.Fg
		}
		if s.Violated {
			fg = pal.FgMute
		}
		add(pad, ink(bg, fg).Render(launder(s.Ledger[i].String(), w-4)))
	}
	return out
}

func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	var line string
	for word := range strings.FieldsSeq(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
