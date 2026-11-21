package app

import (
	"slices"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type sidebarSectionPlan struct {
	Section sidebarSection
	Share   int
	Spacer  bool
}

var sidebarSectionByName = map[string]sidebarSection{
	"sessions":                  sidebarSectionSessions,
	"terminals":                 sidebarSectionTerminals,
	"agents":                    sidebarSectionAgents,
	"files":                     sidebarSectionFiles,
	"git":                       sidebarSectionGit,
	config.SidebarSectionCustom: sidebarSectionCustom,
	config.SidebarSectionPane:   sidebarSectionPane,
}

var sidebarSectionFloors = [sidebarSectionCount]int{
	sidebarSectionSessions:  2,
	sidebarSectionTerminals: 3,
	sidebarSectionAgents:    2,
	sidebarSectionFiles:     2,
	sidebarSectionGit:       2,
	sidebarSectionCustom:    2,
	sidebarSectionPane:      3,
}

var (
	layoutMu     sync.Mutex
	layoutSource string
	layoutParsed []sidebarSectionPlan
	layoutOpen   []sidebarSectionPlan
	layoutHas    [sidebarSectionCount]bool
	layoutValid  bool
)

func sidebarLayoutPlans(s *config.Settings) []sidebarSectionPlan {
	plans, _ := sidebarLayoutFor(s.SidebarSections)
	return plans
}

func (m *OS) sidebarPlans() []sidebarSectionPlan {
	if m.filesView.Show > 0 {
		_, open, _ := sidebarLayoutEntry(m.Settings.SidebarSections)
		return open
	}
	return sidebarLayoutPlans(&m.Settings)
}

func sidebarLayoutWithFiles(source string) string {
	entries := config.ParseSidebarSections(source)
	index := func(name string) int {
		return slices.IndexFunc(entries, func(e config.SidebarSectionShare) bool { return e.Name == name })
	}
	if index("files") >= 0 {
		return config.SidebarSectionsString(entries)
	}
	at := len(entries)
	if i := index("terminals"); i >= 0 {
		at = i + 1
	} else if i := index("sessions"); i >= 0 {
		at = i + 1
	} else if i := index("agents"); i >= 0 {
		at = i
	} else if at > 0 && entries[at-1].IsSpacer() {
		at--
	}
	share := 0
	for _, e := range config.ParseSidebarSections(config.SidebarDefaultSections) {
		if e.Name == "files" {
			share = e.Share
		}
	}
	return config.SidebarSectionsString(slices.Insert(entries, at, config.SidebarSectionShare{Name: "files", Share: share}))
}

func sidebarLayoutHas(section sidebarSection, s *config.Settings) bool {
	_, has := sidebarLayoutFor(s.SidebarSections)
	return has[section]
}

func sidebarLayoutPins(plans []sidebarSectionPlan) bool {
	return len(plans) > 1 && !plans[len(plans)-1].Spacer
}

func sidebarLayoutStacksAbove(plans []sidebarSectionPlan, above, below sidebarSection) bool {
	seenAbove := false
	for _, p := range plans {
		switch {
		case p.Spacer:
		case p.Section == above:
			seenAbove = true
		case p.Section == below:
			return seenAbove
		}
	}
	return false
}

func sidebarLayoutFor(source string) ([]sidebarSectionPlan, [sidebarSectionCount]bool) {
	plans, _, has := sidebarLayoutEntry(source)
	return plans, has
}

func sidebarLayoutEntry(source string) ([]sidebarSectionPlan, []sidebarSectionPlan, [sidebarSectionCount]bool) {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	if !layoutValid || layoutSource != source {
		plans, has := sidebarPlansOf(source)
		open := plans
		if !has[sidebarSectionFiles] {
			open, _ = sidebarPlansOf(sidebarLayoutWithFiles(source))
		}
		layoutSource, layoutParsed, layoutOpen, layoutHas, layoutValid = source, plans, open, has, true
	}
	return layoutParsed, layoutOpen, layoutHas
}

func sidebarPlansOf(source string) ([]sidebarSectionPlan, [sidebarSectionCount]bool) {
	var has [sidebarSectionCount]bool
	entries := config.ParseSidebarSections(source)
	plans := make([]sidebarSectionPlan, 0, len(entries))
	for _, e := range entries {
		if e.IsSpacer() {
			plans = append(plans, sidebarSectionPlan{
				Section: sidebarSectionCount, Share: e.Share, Spacer: true,
			})
			continue
		}
		section, ok := sidebarSectionByName[e.Name]
		if !ok {
			continue
		}
		has[section] = true
		plans = append(plans, sidebarSectionPlan{Section: section, Share: e.Share})
	}
	return plans, has
}

func sidebarBudgetLines(avail int, plans []sidebarSectionPlan, rows, rowH []int) []int {
	avail = max(avail, 0)
	out := make([]int, len(plans))
	capLines := make([]int, len(plans))
	floorLines := make([]int, len(plans))
	used := 0
	flex := 0
	for i, p := range plans {
		h := max(rowH[i], 1)
		if p.Spacer {
			capLines[i] = avail
			if p.Share > 0 {
				out[i] = min(avail, avail*p.Share/100)
				used += out[i]
			}
			continue
		}
		capLines[i] = rows[i] * h
		floorLines[i] = min(capLines[i], sidebarSectionFloors[p.Section]*h)
		if p.Share <= 0 {
			flex++
			continue
		}
		out[i] = min(capLines[i], max(h*(avail*p.Share/100), floorLines[i]))
		used += out[i]
	}
	if flex > 0 {
		left := max(avail-used, 0)
		each, extra := left/flex, left%flex
		for i, p := range plans {
			if p.Share > 0 || p.Spacer {
				continue
			}
			out[i] = each
			if extra > 0 {
				out[i]++
				extra--
			}
		}
	}
	for i, p := range plans {
		if p.Spacer || p.Share > 0 || out[i] >= floorLines[i] {
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
	bare := make([]int, 0, len(plans))
	for i, p := range plans {
		if p.Spacer && p.Share <= 0 {
			bare = append(bare, i)
		}
	}
	if len(bare) > 0 {
		left := avail
		for _, n := range out {
			left -= n
		}
		if left > 0 {
			each, extra := left/len(bare), left%len(bare)
			for _, i := range bare {
				out[i] = each
				if extra > 0 {
					out[i]++
					extra--
				}
			}
		}
	}

	last := len(plans) - 1
	if !sidebarLayoutPins(plans) {
		last = -1
	}
	grow := make([]int, 0, len(plans))
	for i, p := range plans {
		if !p.Spacer && p.Share <= 0 && i != last {
			grow = append(grow, i)
		}
	}
	for i, p := range plans {
		if !p.Spacer && p.Share > 0 && i != last {
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
	order := make([]int, 0, len(plans))
	for i, plan := range slices.Backward(plans) {
		if plan.Spacer {
			order = append(order, i)
		}
	}
	for i, plan := range slices.Backward(plans) {
		if !plan.Spacer && plan.Share > 0 {
			order = append(order, i)
		}
	}
	for i, plan := range slices.Backward(plans) {
		if !plan.Spacer && plan.Share <= 0 {
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

func (m *OS) sidebarScrollOffsets() [sidebarSectionCount]*int {
	out := [sidebarSectionCount]*int{}
	for i := range out {
		out[i] = &m.SidebarScroll[i]
	}
	return out
}
