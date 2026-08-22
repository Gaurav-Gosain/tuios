package session

import (
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A theme is the half of the appearance that list-options cannot describe. The
// other 88 settings are scalars with a closed set of values, and the registry
// says what each one takes; a theme is a name drawn from a list of hundreds
// that grows whenever the user writes a file, standing for twenty colours kept
// in a different format in a different directory. Published as a bare string
// option it was undiscoverable and unverifiable at once: nothing said which
// names resolve, and setting one that did not reported success.
//
// This verb is the three questions an agent has to be able to ask about that:
// which themes are there, what is this one actually made of, and is what I just
// chose legible.

// maxThemeList caps how many ids are returned unfiltered. The registry holds
// several hundred and a caller that wants a particular one is better served by
// filtering for it than by paging through them; the total is always reported,
// so a truncated list says so rather than looking complete.
const maxThemeList = 100

// verbListThemes reports the registered themes, and describes one when asked.
func (d *Daemon) verbListThemes(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Theme   string `json:"theme"`
		Filter  string `json:"filter"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	// The themes directory is re-read on every call. It is a handful of small
	// files, and the alternative is reporting a stale list to the one caller
	// most likely to have just written to it.
	_, problems := theme.ReloadCustomThemes()

	all := theme.AvailableThemes()
	themesDir, dirErr := theme.GetThemesDir()

	out := map[string]any{
		"type":       "theme_list",
		"total":      len(all),
		"themes_dir": themesDir,
	}
	if dirErr != nil {
		out["themes_dir_error"] = dirErr.Error()
	}
	// A malformed theme file is the likeliest reason a theme a caller expects is
	// missing, and it is otherwise only ever a line in a log nobody outside the
	// process can read.
	if len(problems) > 0 {
		out["problems"] = problems
	}

	// The theme in effect for this session, read from what the session was told
	// rather than from this process's active tint: the daemon does not draw, and
	// its own tint is not the attached client's.
	if sess := d.findTargetSession(p.Session); sess != nil {
		out["session"] = sess.Name
		if v, ok := sess.GetOption("appearance.theme"); ok {
			out["active"] = v
			out["active_source"] = "session"
		} else if opt, ok := config.LookupOption("appearance.theme"); ok {
			out["active"] = opt.Default
			out["active_source"] = "default"
		}
	} else if p.Session != "" {
		return nil, mustResolve(d, p.Session)
	}

	// A caller that named a theme asked about that theme. Answering with a
	// hundred other ids buries the palette it wanted under the list it did not,
	// so the roster is sent only when it is the question: no theme named, or a
	// filter given alongside one.
	matched := all
	if p.Filter != "" {
		needle := strings.ToLower(p.Filter)
		matched = matched[:0:0]
		for _, id := range all {
			if strings.Contains(strings.ToLower(id), needle) {
				matched = append(matched, id)
			}
		}
	}
	out["matched"] = len(matched)
	if p.Theme == "" || p.Filter != "" {
		if len(matched) > maxThemeList {
			out["themes"] = matched[:maxThemeList]
			out["truncated"] = true
		} else {
			out["themes"] = matched
		}
	}

	if p.Theme != "" {
		pal, ok := theme.Describe(p.Theme)
		if !ok {
			return nil, hintedVerbError(ErrVerbOptionNotFound, "no theme named "+echoName(p.Theme), &VerbHint{
				Param:      "theme",
				Verb:       "list-themes",
				Command:    "tuios list-themes --filter " + p.Theme,
				DidYouMean: closestMatch(p.Theme, all),
				Detail: "the id is in neither the built-in registry nor " + themesDir +
					"; write <id>.json there to add it.",
			})
		}
		out["palette"] = pal
	}

	return out, nil
}
