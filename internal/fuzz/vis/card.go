package vis

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// The end card. It is the frame a recording ends on and the frame somebody
// shares, so every figure on it comes straight off the Result the engine
// returned. On a failure it carries the seed and the minimal script, which
// makes it a complete claim: a reader can run the command and get the same
// failure.

// overlayCard centres the verdict panel over a ghost of the final frame.
func overlayCard(lines []string, s Snapshot, o Options, pal overlay.Palette, w, h int) []string {
	back := ghost(lines, pal)
	card, _ := cardPanel(s, o, pal, w).Render(pal)
	rows := strings.Split(card, "\n")

	top := max((h-len(rows))/2, 0)
	for i, r := range rows {
		y := top + i
		if y < 0 || y >= len(back) {
			continue
		}
		cw := lipgloss.Width(r)
		x := max((w-cw)/2, 0)
		// The backdrop has to survive on both sides of the card. Keeping only
		// the left of it blanked the rest of the row, which read as the frame
		// having been cleared to draw the card rather than the card sitting on
		// the run it is reporting.
		left := ansi.Truncate(back[y], x, "")
		right := ansi.TruncateLeft(back[y], x+cw, "")
		back[y] = overlay.Fill(ansi.Truncate(left+r+right, w, ""), w, pal.Canvas)
	}
	return back
}

func cardPanel(s Snapshot, o Options, pal overlay.Palette, w int) overlay.Panel {
	g := glyphs()
	width := overlay.FitWidth(72, w)
	bg := pal.Surface
	label := ink(bg, pal.FgDim)
	value := ink(bg, pal.Fg)

	var b strings.Builder
	line := func(parts ...string) { b.WriteString(strings.Join(parts, "") + "\n") }

	r := s.Result
	title := "run held"
	if r.Failed {
		title = "run falsified"
		line(bold(bg, pal.Warn).Render(g.fail + " falsified " + g.sep + " " + r.Violations[0].Rule))
		for _, l := range wrap(launder(r.Violations[0].Detail, 4096), width) {
			line(value.Render(l))
		}
	} else {
		// FgMute measures 1.81:1 on Surface, which is the exact failure the
		// polish pass caught, so card text never uses it.
		line(bold(bg, pal.Success).Render(g.pass + " " + itoa(len(s.Rules)) + " invariants held"))
	}
	line("")

	stat := func(k, v string) {
		line(label.Render(k+strings.Repeat(" ", max(12-len(k), 1))), value.Render(v))
	}
	stat("seed", hex16(s.Seed))
	stat("actions", commas(s.Actions)+" executed "+g.sep+" "+commas(r.Replays)+" replays")
	stat("checks", commas(s.Checks))
	if r.Failed {
		stat("shrink", commas(s.InitialLen)+" "+g.arrow+" "+commas(len(r.Actions))+
			" in "+commas(s.RunsSeen)+" candidates")
	}
	stat("elapsed", duration(s.Elapsed))

	if r.Failed {
		line("")
		line(label.Render("minimal reproduction"))
		for i, a := range r.Actions {
			if i >= 8 {
				line(label.Render("  " + g.sep + " " + itoa(len(r.Actions)-8) + " more"))
				break
			}
			line(label.Render("  "+itoa(i+1)+". "), value.Render(launder(a.String(), width-8)))
		}
		if o.Command != "" {
			line("")
			line(ink(bg, pal.AccentBright).Render(o.Command))
		}
	}

	hints := []overlay.Hint{{Key: "r", Label: "run again"}, {Key: "q", Label: "quit"}}
	return overlay.Panel{
		Title: title,
		Width: width,
		Body:  strings.TrimRight(b.String(), "\n"),
		Hints: hints,
	}
}

// hex16 is the seed as the sixteen hex digits the replay command carries, so
// the number on the card is the number a reader types.
func hex16(v uint64) string {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[v&0xf]
		v >>= 4
	}
	return string(buf[:])
}

func duration(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return itoa(int(d/time.Minute)) + "m" + itoa(int((d%time.Minute)/time.Second)) + "s"
	case d >= time.Second:
		return itoa(int(d/time.Second)) + "." + itoa(int((d%time.Second)/(100*time.Millisecond))) + "s"
	default:
		return itoa(int(d/time.Millisecond)) + "ms"
	}
}

// rate is the trailing measured throughput: actions divided by the elapsed time
// of the run so far. Under measureFloor there is not enough elapsed time for
// the figure to mean anything, and a run that has been going for a millisecond
// will happily report three million actions a second, so it says so instead.
const measureFloor = 250 * time.Millisecond

func rate(v float64, d time.Duration, suffix string) string {
	if d < measureFloor {
		return "measuring"
	}
	return commas(int(v)) + suffix
}
