package app

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The rail's layout: which sections it stacks, in what order, and the share of
// it each one may claim.
//
// It used to be three sections in a fixed order with the split written into
// sidebarBudget's arithmetic. A fourth section made that untenable twice over:
// the order stopped being obvious (does a listing belong above the panes it is
// about, or below them?), and the split stopped being one anybody but the
// author would agree with. Both are now read off one string.
//
// # Shares are ceilings, not reservations
//
// This is the property that keeps a four-section rail usable. A share says how
// much of the rail a section may take, never how much it is given: a section
// with three rows takes three lines whatever its share says, and the rest goes
// to the flexible section. So the default's 25 + 25 + 34 does not leave the
// terminals list a sixth of the rail; it leaves it everything the other three
// have no rows for, which on a normal screen is most of it.
//
// A share is also not a floor. Lines nobody claimed go to whichever section
// still has rows below its own fold, so a listing of two hundred names fills a
// rail with room to spare rather than showing a quarter of it over thirty blank
// lines. A rail whose lists all fit keeps the gap it always had.
//
// # What gives way when the rail is too short
//
// The same ladder as before, generalised. A flexible section below its floor
// takes lines back from the sections with shares, the last in the layout first,
// none of them going below its own floor. If the floors still do not fit, lines
// are given up one at a time from the same end. A section is never given more
// lines than its rows can fill and the total is never more than the rail has,
// so two sections cannot land on the same line at any height.

// sidebarSectionPlan is one entry of the rail's layout.
type sidebarSectionPlan struct {
	Section sidebarSection
	// Share is the percent of the rail's content lines the section may claim,
	// or zero for a flexible section that takes what the others leave.
	Share int
}

// sidebarSectionByName maps a layout name to the section it selects. It is the
// one place config's spelling of a section and the renderer's meet.
var sidebarSectionByName = map[string]sidebarSection{
	"sessions":  sidebarSectionSessions,
	"terminals": sidebarSectionTerminals,
	"agents":    sidebarSectionAgents,
	"files":     sidebarSectionFiles,
}

// sidebarSectionFloors is the least each section is shrunk to, in rows, before
// the ladder starts taking whole lines off the end of the rail. Terminals keeps
// three because it is the list the user works in; the rest keep two, which is
// enough for a row and the "+N" that owns up to the ones below it.
var sidebarSectionFloors = [sidebarSectionCount]int{
	sidebarSectionSessions:  2,
	sidebarSectionTerminals: 3,
	sidebarSectionAgents:    2,
	sidebarSectionFiles:     2,
}

// The parsed layout, cached against the string it came from. The rail parses it
// on every frame it rebuilds, and a split plus an Atoi per frame is a cost the
// render cache exists to avoid paying; the mutex is here because tests render
// rails in parallel.
var (
	layoutMu     sync.Mutex
	layoutSource string
	layoutParsed []sidebarSectionPlan
	layoutHas    [sidebarSectionCount]bool
	layoutValid  bool
)

// sidebarLayoutPlans is the rail's layout, in the order the sections are
// stacked from the top.
func sidebarLayoutPlans() []sidebarSectionPlan {
	plans, _ := sidebarLayoutFor(config.SidebarSections)
	return plans
}

// sidebarLayoutHas reports whether the layout names a section at all.
func sidebarLayoutHas(section sidebarSection) bool {
	_, has := sidebarLayoutFor(config.SidebarSections)
	return has[section]
}

func sidebarLayoutFor(source string) ([]sidebarSectionPlan, [sidebarSectionCount]bool) {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	if !layoutValid || layoutSource != source {
		var has [sidebarSectionCount]bool
		entries := config.ParseSidebarSections(source)
		plans := make([]sidebarSectionPlan, 0, len(entries))
		for _, e := range entries {
			section, ok := sidebarSectionByName[e.Name]
			if !ok {
				continue
			}
			has[section] = true
			plans = append(plans, sidebarSectionPlan{Section: section, Share: e.Share})
		}
		layoutSource, layoutParsed, layoutHas, layoutValid = source, plans, has, true
	}
	return layoutParsed, layoutHas
}

// sidebarBudgetLines divides avail lines between the sections of a layout.
//
// rows is how many rows each section has, rowH how many lines one of its rows
// takes. The result is one line count per plan entry, in the plan's own order.
//
// The three guarantees, in the order they are enforced below: a section never
// claims lines for rows it does not have, a flexible section keeps its floor
// where the sections with shares can spare it, and the total never exceeds
// avail. The last one is unconditional, which is what stops two sections
// landing on the same line of a rail too short for either.
func sidebarBudgetLines(avail int, plans []sidebarSectionPlan, rows, rowH []int) []int {
	avail = max(avail, 0)
	out := make([]int, len(plans))
	capLines := make([]int, len(plans))
	floorLines := make([]int, len(plans))
	used := 0
	flex := 0
	for i, p := range plans {
		h := max(rowH[i], 1)
		capLines[i] = rows[i] * h
		floorLines[i] = min(capLines[i], sidebarSectionFloors[p.Section]*h)
		if p.Share <= 0 {
			flex++
			continue
		}
		out[i] = min(capLines[i], max(h*(avail*p.Share/100), floorLines[i]))
		used += out[i]
	}
	// What the shares left over, split between the flexible sections. Not
	// capped by their rows yet: the ladder below needs to see the shortfall a
	// section with more rows than lines is actually in.
	if flex > 0 {
		left := max(avail-used, 0)
		each, extra := left/flex, left%flex
		for i, p := range plans {
			if p.Share > 0 {
				continue
			}
			out[i] = each
			if extra > 0 {
				out[i]++
				extra--
			}
		}
	}
	// The ladder: a flexible section under its floor takes lines back from the
	// sections with shares, last in the layout first, none of them below its
	// own floor.
	for i, p := range plans {
		if p.Share > 0 || out[i] >= floorLines[i] {
			continue
		}
		for j := len(plans) - 1; j >= 0 && out[i] < floorLines[i]; j-- {
			if plans[j].Share <= 0 {
				continue
			}
			if take := min(out[j]-floorLines[j], floorLines[i]-out[i]); take > 0 {
				out[j] -= take
				out[i] += take
			}
		}
	}
	for i := range out {
		out[i] = max(min(out[i], capLines[i]), 0)
	}
	// Lines nobody claimed, handed to the sections that still have rows below
	// their own fold.
	//
	// This is what stops a share reading as a reservation on a rail with room to
	// spare. A directory of two hundred names against a quarter share would
	// otherwise show eight of them over thirty blank lines, which is the rail
	// refusing to use space it has and nobody else wants. A section already
	// showing every row it has is not grown, so a rail whose lists all fit keeps
	// the gap it always had and the pinned block keeps floating at the bottom.
	//
	// Flexible sections are offered the spare lines first, because "the terminals
	// list takes the slack" is the rule the rail was built on. After that it is
	// round robin in layout order, so two hungry sections split what is left
	// rather than the first one taking all of it.
	grow := make([]int, 0, len(plans))
	for i, p := range plans {
		if p.Share <= 0 {
			grow = append(grow, i)
		}
	}
	for i, p := range plans {
		if p.Share > 0 {
			grow = append(grow, i)
		}
	}
	for {
		left := avail
		for _, n := range out {
			left -= n
		}
		gave := false
		for _, i := range grow {
			h := max(rowH[i], 1)
			if left < h || out[i]+h > capLines[i] {
				continue
			}
			out[i] += h
			left -= h
			gave = true
		}
		if !gave {
			break
		}
	}
	// The floors can still overrun a rail with almost no lines at all, so the
	// last word belongs to the space that exists: give it up from the quietest
	// end outwards, which is the sections with shares in reverse order and only
	// then the flexible ones.
	order := make([]int, 0, len(plans))
	for i := len(plans) - 1; i >= 0; i-- {
		if plans[i].Share > 0 {
			order = append(order, i)
		}
	}
	for i := len(plans) - 1; i >= 0; i-- {
		if plans[i].Share <= 0 {
			order = append(order, i)
		}
	}
	total := 0
	for _, n := range out {
		total += n
	}
	for total > avail {
		gave := false
		for _, i := range order {
			if out[i] > 0 {
				out[i]--
				total--
				gave = true
				break
			}
		}
		if !gave {
			break
		}
	}
	return out
}

// sidebarScrollOffsets is each section's own scroll offset, indexed by section.
// One place rather than three copies of the same array literal: the wheel, the
// cursor auto-scroll and the render each need it, and a fourth section added to
// two of the three is how a section ends up unable to scroll.
func (m *OS) sidebarScrollOffsets() [sidebarSectionCount]*int {
	return [sidebarSectionCount]*int{
		sidebarSectionSessions:  &m.SidebarScrollS,
		sidebarSectionTerminals: &m.SidebarScrollT,
		sidebarSectionAgents:    &m.SidebarScrollA,
		sidebarSectionFiles:     &m.SidebarScrollF,
	}
}
