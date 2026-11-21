package session

import (
	"encoding/json"
	"maps"
	"sort"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (d *Daemon) verbListGlyphs(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Glyphs  string `json:"glyphs"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

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

	sess := d.findTargetSession(cs, p.Session)
	opts := d.optionsFor(cs, sess)
	if sess != nil {
		out["session"] = sess.Name()
		if v, source, ok := opts.lookup("appearance.glyphs"); ok {
			out["active"] = v
			out["active_source"] = source
		}
	} else if p.Session != "" {
		return nil, mustResolve(d, cs, p.Session)
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
		borderStyle:    opts.get("appearance.border_style"),
		scrollbarStyle: opts.get("appearance.scrollbar.style"),
		scrollbarThumb: opts.get("appearance.scrollbar.thumb"),
		scrollbarTrack: opts.get("appearance.scrollbar.track"),
	})
	return out, nil
}

type glyphRenderSettings struct {
	borderStyle    string
	scrollbarStyle string
	scrollbarThumb string
	scrollbarTrack string
}

var describeMu sync.Mutex

func describeGlyphSet(id string, render glyphRenderSettings) map[string]any {
	set := theme.ResolveGlyphSet(id)
	named := map[string]string{}
	maps.Copy(named, glyphSetNamedRoles(set))

	settings := config.DefaultSettings()
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
		"id":               id,
		"display_name":     set.DisplayName,
		"inherits":         set.Inherits,
		"border_style":     render.borderStyle,
		"border_in_effect": render.borderStyle == config.BorderStyleGlyphs,
		"ascii":            drawnIsASCII(drawn),
		"names":            named,
		"drawn":            drawn,
	}
}

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

func glyphRoleNames() []string {
	names := theme.GlyphRoleNames()
	for _, b := range theme.GlyphBorderRoleNames() {
		names = append(names, "border."+b)
	}
	sort.Strings(names)
	return names
}

func drawnIsASCII(drawn map[string]string) bool {
	for _, glyph := range drawn {
		if !overlay.IsASCII(glyph) {
			return false
		}
	}
	return true
}
