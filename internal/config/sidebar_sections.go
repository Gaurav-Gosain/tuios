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
// and a repeated name are all dropped rather than refused, because the rail
// draws from this on every frame and a rail that refuses to lay itself out is
// worse than one that lays itself out slightly differently from what was typed.
// SidebarSectionProblems is where a person is told what was wrong. A string
// with nothing usable in it falls back to the shipped layout, since a rail with
// no sections at all is not a state anybody meant to ask for.
func ParseSidebarSections(source string) []SidebarSectionShare {
	out := make([]SidebarSectionShare, 0, len(SidebarSectionNames))
	seen := make([]string, 0, len(SidebarSectionNames))
	for _, field := range strings.Split(source, ",") {
		name, share, _ := strings.Cut(strings.TrimSpace(field), ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.Contains(SidebarSectionNames, name) || slices.Contains(seen, name) {
			continue
		}
		entry := SidebarSectionShare{Name: name}
		if pct, err := strconv.Atoi(strings.TrimSpace(share)); err == nil {
			entry.Share = max(min(pct, 100), 0)
		}
		seen = append(seen, name)
		out = append(out, entry)
	}
	if len(out) == 0 && source != SidebarDefaultSections {
		return ParseSidebarSections(SidebarDefaultSections)
	}
	return out
}

// SidebarSectionProblems reports what a layout string asked for and did not
// get, one line per problem, for the config validator to print.
func SidebarSectionProblems(source string) []string {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var problems []string
	var seen []string
	usable := 0
	for _, field := range strings.Split(source, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		name, share, hasShare := strings.Cut(field, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case !slices.Contains(SidebarSectionNames, name):
			problems = append(problems, "there is no rail section called "+strconv.Quote(name)+
				". The sections are "+strings.Join(SidebarSectionNames, ", "))
			continue
		case slices.Contains(seen, name):
			problems = append(problems, name+" is listed twice, so the second one is ignored")
			continue
		}
		seen = append(seen, name)
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
		usable++
	}
	if usable == 0 {
		problems = append(problems, "no section in "+strconv.Quote(source)+
			" could be used, so the rail keeps its default layout")
	}
	return problems
}
