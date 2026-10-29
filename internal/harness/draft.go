package harness

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Drafts: whether the person has text waiting in the agent's input box.
//
// The idle rules prove the box is on the screen. They do not prove it is
// empty: Claude Code's idle rule reads "^\s*❯" in the box, and a half-written
// prompt starts with the same character. A queued message typed into that box
// lands in the middle of the person's draft. So a manifest may say what its box
// looks like with nothing in it, and the queue holds its entries while the box
// looks like anything else.
//
// A manifest without the block cannot tell, and BoxDraft says so: the queue
// then waits only for the person to stop typing.

// BoxRule is a manifest's [prompt_box] block: where the input box is, and what
// it reads when it is empty.
type BoxRule struct {
	// Region is the screen region that holds the box (region.go). A screen
	// where that region is missing has no box to judge, and BoxDraft says it
	// cannot tell.
	Region string `toml:"region"`
	// Empty holds patterns for the region's text with nothing typed in it:
	// its lines with blank ones dropped, joined with newlines. The box is
	// empty when one of them matches. Write them anchored, with \A and \z,
	// since a pattern that matches part of the text matches a draft too.
	Empty []string `toml:"empty"`
	// Source says how the patterns were obtained, the way [input] does:
	// measured on a release, or derived from a description.
	Source string `toml:"source"`

	empty []*regexp.Regexp
}

// check normalises the region and compiles the patterns. lines is how far up
// the manifest's screen reads.
func (b *BoxRule) check(lines int, screenOn bool) error {
	if len(b.Empty) == 0 {
		if strings.TrimSpace(b.Region) != "" {
			return fmt.Errorf("region without empty patterns")
		}
		return nil
	}
	if !screenOn {
		return fmt.Errorf("needs [screen] enabled, which reads the box")
	}
	region, bottom, err := normalizeScreenRegion(b.Region)
	if err != nil {
		return err
	}
	if bottom > lines {
		return fmt.Errorf("region %s reads past the %d lines the manifest reads; raise [screen] lines", region, lines)
	}
	b.Region = region
	res, err := compilePatterns(b.Empty)
	if err != nil {
		return err
	}
	b.empty = res
	return nil
}

// HasBoxRule reports whether the harness says what its empty input box looks
// like. A nil registry knows no harness.
func (r *Registry) HasBoxRule(id string) bool {
	if r == nil {
		return false
	}
	m := r.Lookup(id)
	return m != nil && len(m.Box.empty) > 0
}

// BoxDraft reads the harness's input box. tail is the pane's tail as TailText
// reads it, and shown is the same lines with every cell the program drew faint
// left blank, or nil to read tail itself. Placeholders and suggestions are
// drawn faint (Codex's "Explain this codebase", Claude Code's suggested next
// prompt), so the person's own text is what shown keeps. known is false when
// the harness has no [prompt_box] block or the screen shows no box, and draft
// is then false.
func (r *Registry) BoxDraft(id string, tail, shown []string) (draft, known bool) {
	if r == nil {
		return false, false
	}
	m := r.Lookup(id)
	if m == nil || len(m.Box.empty) == 0 {
		return false, false
	}
	if len(shown) != len(tail) {
		shown = tail
	}
	lo, hi, ok := regionBounds(tail, m.Box.Region)
	if !ok {
		return false, false
	}
	if m.Box.Region == RegionPromptBox && lo == hi {
		// Two rules with nothing between them: no box.
		return false, false
	}
	var kept []string
	for _, line := range shown[lo:hi] {
		if strings.TrimFunc(line, unicode.IsSpace) != "" {
			kept = append(kept, strings.TrimRightFunc(line, unicode.IsSpace))
		}
	}
	text := strings.Join(kept, "\n")
	for _, re := range m.Box.empty {
		if re.MatchString(text) {
			return false, true
		}
	}
	return true, true
}
