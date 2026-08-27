package session

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A glyph set is the shape half of a rice and it has list-themes's problem: its
// value is a name from an open set that grows whenever the user writes a file,
// standing for a document kept in another format in another directory. Published
// as a bare string option it would be undiscoverable and unverifiable at once,
// so it gets the verb the palette got, answering the same three questions.
//
// The third of them is the one that matters most here and has no equivalent for
// a theme. A set says only what it changes, so "what will this look like" cannot
// be read off the file; and a role whose glyph was the wrong width is dropped
// silently on screen. Both are answered by reporting what is actually drawn
// rather than what the set asked for.

// verbListGlyphs reports the glyph sets, and describes one when asked.
func (d *Daemon) verbListGlyphs(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Glyphs  string `json:"glyphs"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	// Re-read every call, like the themes directory and for the same reason:
	// the caller most likely to ask is the one that has just written the file.
	_, problems := theme.ReloadGlyphSets()

	all := theme.AvailableGlyphSets()
	glyphsDir, dirErr := theme.GetGlyphsDir()

	out := map[string]any{
		"type":       "glyph_set_list",
		"total":      len(all),
		"glyphs_dir": glyphsDir,
		"sets":       all,
		"roles":      glyphRoleNames(),
	}
	if dirErr != nil {
		out["glyphs_dir_error"] = dirErr.Error()
	}
	if len(problems) > 0 {
		out["problems"] = problems
	}

	if sess := d.findTargetSession(p.Session); sess != nil {
		out["session"] = sess.Name
		if v, ok := sess.GetOption("appearance.glyphs"); ok {
			out["active"] = v
			out["active_source"] = "session"
		} else if opt, ok := config.LookupOption("appearance.glyphs"); ok {
			out["active"] = opt.Default
			out["active_source"] = "default"
		}
	} else if p.Session != "" {
		return nil, mustResolve(d, p.Session)
	}

	if p.Glyphs == "" {
		return out, nil
	}
	if !theme.GlyphSetExists(p.Glyphs) {
		return nil, hintedVerbError(ErrVerbOptionNotFound, "no glyph set named "+echoName(p.Glyphs), &VerbHint{
			Param:      "glyphs",
			Verb:       "list-glyphs",
			Command:    "tuios list-glyphs",
			DidYouMean: closestMatch(p.Glyphs, all),
			Available:  all,
			Detail: "the id is neither built in nor in " + glyphsDir +
				"; write <id>.json there to add it.",
		})
	}
	out["set"] = describeGlyphSet(p.Glyphs, glyphRenderSettings{
		borderStyle:    d.effectiveOption(p.Session, "appearance.border_style"),
		scrollbarStyle: d.effectiveOption(p.Session, "appearance.scrollbar.style"),
		scrollbarThumb: d.effectiveOption(p.Session, "appearance.scrollbar.thumb"),
		scrollbarTrack: d.effectiveOption(p.Session, "appearance.scrollbar.track"),
	})
	return out, nil
}

// effectiveOption is what a session would draw for an option: what it was told,
// falling back to what the option does untold.
//
// The daemon's own config globals are not that. The daemon does not draw, so it
// never applies an appearance config, and reading its BorderStyle reported the
// empty string for every session including the ones that had set one.
func (d *Daemon) effectiveOption(sessionName, path string) string {
	if sess := d.findTargetSession(sessionName); sess != nil {
		if v, ok := sess.GetOption(path); ok {
			return v
		}
	}
	if opt, ok := config.LookupOption(path); ok {
		return opt.Default
	}
	return ""
}

// glyphRenderSettings is what a session's other options say about the glyphs
// this set would draw. The daemon does not draw, so its own copies of these
// globals were never applied and reading them reported the defaults for every
// session including the ones that had set something.
type glyphRenderSettings struct {
	borderStyle    string
	scrollbarStyle string
	scrollbarThumb string
	scrollbarTrack string
}

// describeMu serializes the borrowed selection below. Every connection runs on
// its own goroutine, so two list-glyphs calls naming different sets would
// otherwise each read the other's "previous", hand back a mixed table, and
// leave the process on whichever set lost the race.
var describeMu sync.Mutex

// describeGlyphSet reports one set: what it names, and what would actually be
// drawn if it were selected.
//
// "Drawn" is answered by selecting it, reading the resolved glyphs and putting
// the previous selection back. That is the honest answer and the only one that
// cannot drift: the accessors it reads through are the same ones the renderer
// calls, so a role this reports is a role that draws. It is safe because the
// daemon does not draw - the selection it borrows is process-local state that
// no frame of any attached client is composed from.
func describeGlyphSet(id string, render glyphRenderSettings) map[string]any {
	set := theme.ResolveGlyphSet(id)
	named := map[string]string{}
	for role, glyph := range glyphSetNamedRoles(set) {
		named[role] = glyph
	}

	// The settings the glyphs are resolved against are built here and thrown
	// away, so nothing in the process ever sees them. This used to be a
	// save/set/restore over the package globals: describeMu made it safe
	// against a second describe call and against nothing else, and a client
	// composing a frame on another goroutine could read the borrowed border
	// style mid-flight. A local value has no such window.
	//
	// The glyph set itself is still borrowed, because theme keeps it in a
	// package global that has no per-call form yet; describeMu still guards
	// that half.
	settings := config.Global
	settings.BorderStyle = render.borderStyle
	settings.ScrollbarStyle = render.scrollbarStyle
	settings.ScrollbarThumb, settings.ScrollbarTrack = render.scrollbarThumb, render.scrollbarTrack

	describeMu.Lock()
	prev := theme.ActiveGlyphSetID()
	theme.SetActiveGlyphs(id)
	drawn := settings.ResolvedGlyphs()
	theme.SetActiveGlyphs(prev)
	describeMu.Unlock()

	return map[string]any{
		"id":           id,
		"display_name": set.DisplayName,
		"inherits":     set.Inherits,
		// border_style says whether the border rows of drawn are actually on
		// screen. A set's border is selected by name rather than winning
		// silently, so a set can carry one that nothing is currently drawing.
		"border_style":     render.borderStyle,
		"border_in_effect": render.borderStyle == config.BorderStyleGlyphs,
		// Whether the frame this set draws is 7-bit throughout, which is what
		// makes it offerable to a terminal that manages nothing else. Measured
		// over the resolved glyphs rather than the set's own fields: a role the
		// set leaves alone is empty and so trivially ASCII, while the built-in
		// it falls back to may not be, and a set that named two ASCII glyphs
		// was reporting itself drawable anywhere.
		"ascii": drawnIsASCII(drawn),
		"names": named,
		"drawn": drawn,
	}
}

// glyphSetNamedRoles is the roles this set says something about, as opposed to
// the ones it leaves to the default.
func glyphSetNamedRoles(set *theme.GlyphSet) map[string]string {
	named := map[string]string{}
	for role, glyph := range theme.GlyphSetRoles(set) {
		if glyph != "" {
			named[role] = glyph
		}
	}
	if set.Border != nil {
		for role, glyph := range theme.GlyphSetBorderRoles(set.Border) {
			if glyph != "" {
				named["border."+role] = glyph
			}
		}
	}
	return named
}

// glyphRoleNames is every role a set can name, sorted, so a caller writing one
// does not have to guess the spelling or read the source.
func glyphRoleNames() []string {
	names := theme.GlyphRoleNames()
	for _, b := range theme.GlyphBorderRoleNames() {
		names = append(names, "border."+b)
	}
	sort.Strings(names)
	return names
}

// drawnIsASCII reports whether every glyph the set resolves to is 7-bit.
//
// The border rows are included even when border_style is not asking for them,
// because the question is whether this set could be used on such a terminal,
// and selecting its border is one more call away.
func drawnIsASCII(drawn map[string]string) bool {
	for _, glyph := range drawn {
		if !overlay.IsASCII(glyph) {
			return false
		}
	}
	return true
}
