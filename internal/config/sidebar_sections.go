package config

import (
	"slices"
	"strconv"
	"strings"
)

// The rail's layout string, appearance.sidebar.sections.
//
// The syntax lives here rather than in the renderer because two callers need
// it and they must agree: the rail, which lays itself out from it on every
// frame, and the config validator, which tells a person what they typed wrong.
// A second parser in the validator would be a second opinion about what the
// string means.

// SidebarSectionShare is one entry of the layout: a section, and the share of
// the rail it may claim.
type SidebarSectionShare struct {
	Name string
	// Share is the percent of the rail's content lines the section may take, or
	// zero for a flexible section that takes what the others leave.
	Share int
}

// ParseSidebarSections reads "name:percent,name,..." into a layout.
//
// It is forgiving on purpose: an unknown name, a percent that is not a number
// and a repeated section name are all dropped rather than refused, because the
// rail draws from this on every frame and a rail that refuses to lay itself out
// is worse than one that lays itself out slightly differently from what was
// typed. SidebarSectionProblems is where a person is told what was wrong. A
// string with no section in it falls back to the shipped layout, since a rail
// of nothing but empty blocks is not a state anybody meant to ask for.
//
// The spacer is the exception to the repeat rule. It names a place rather than
// a section, so a layout may carry as many as it likes and each one is its own
// entry in the result.
func ParseSidebarSections(source string) []SidebarSectionShare {
	out := make([]SidebarSectionShare, 0, len(SidebarSectionNames)+1)
	seen := make([]string, 0, len(SidebarSectionNames))
	sections := 0
	for _, field := range strings.Split(source, ",") {
		name, share, _ := strings.Cut(strings.TrimSpace(field), ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != SidebarSectionSpacer {
			if !slices.Contains(SidebarSectionNames, name) || slices.Contains(seen, name) {
				continue
			}
			seen = append(seen, name)
			sections++
		}
		entry := SidebarSectionShare{Name: name}
		if pct, err := strconv.Atoi(strings.TrimSpace(share)); err == nil {
			entry.Share = max(min(pct, 100), 0)
		}
		out = append(out, entry)
	}
	if sections == 0 && source != SidebarDefaultSections {
		return ParseSidebarSections(SidebarDefaultSections)
	}
	return out
}

// IsSpacer reports whether this entry is the layout's empty block rather than
// one of the rail's sections.
func (s SidebarSectionShare) IsSpacer() bool { return s.Name == SidebarSectionSpacer }

// SidebarSectionProblems reports what a layout string asked for and did not
// get, one line per problem, for the config validator to print.
func SidebarSectionProblems(source string) []string {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var problems []string
	var seen []string
	sections := 0
	for _, field := range strings.Split(source, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		name, share, hasShare := strings.Cut(field, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != SidebarSectionSpacer {
			switch {
			case !slices.Contains(SidebarSectionNames, name):
				problems = append(problems, "there is no rail section called "+strconv.Quote(name)+
					". The names are "+strings.Join(SidebarLayoutNames(), ", "))
				continue
			case slices.Contains(seen, name):
				problems = append(problems, name+" is listed twice, so the second one is ignored")
				continue
			}
			seen = append(seen, name)
			sections++
		}
		if hasShare {
			pct, err := strconv.Atoi(strings.TrimSpace(share))
			switch {
			case err != nil:
				problems = append(problems, name+" has a share of "+strconv.Quote(strings.TrimSpace(share))+
					", which is not a whole number, so it takes what the other sections leave")
			case pct < 0 || pct > 100:
				problems = append(problems, name+" has a share of "+strconv.Itoa(pct)+
					" percent, which is clamped to 0..100")
			}
		}
	}
	if sections == 0 {
		problems = append(problems, "no section in "+strconv.Quote(source)+
			" could be used, so the rail keeps its default layout")
	}
	return problems
}

// SidebarSectionsWithout returns the layout with one section taken out of it,
// keeping the order and the shares of the rest.
//
// This is what the deprecated show_windows and show_agents booleans fold into.
// It is spelled here rather than at the call site because the layout's grammar
// lives here, and a second place that knows how to write one would be a second
// opinion about what it means.
func SidebarSectionsWithout(source, section string) string {
	entries := ParseSidebarSections(source)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name == section {
			continue
		}
		out = append(out, e.String())
	}
	return strings.Join(out, ",")
}

// String writes one entry back in the grammar it was read from.
func (s SidebarSectionShare) String() string {
	if s.Share <= 0 {
		return s.Name
	}
	return s.Name + ":" + strconv.Itoa(s.Share)
}

// SidebarSectionsString writes a whole layout back out.
func SidebarSectionsString(entries []SidebarSectionShare) string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.String())
	}
	return strings.Join(out, ",")
}
