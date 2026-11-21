package session

import (
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const maxThemeList = 100

func (d *Daemon) verbListThemes(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Theme   string `json:"theme"`
		Filter  string `json:"filter"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

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
	if len(problems) > 0 {
		out["problems"] = problems
	}

	if sess := d.findTargetSession(cs, p.Session); sess != nil {
		out["session"] = sess.Name()
		if v, source, ok := d.optionsFor(cs, sess).lookup("appearance.theme"); ok {
			out["active"] = v
			out["active_source"] = source
		}
	} else if p.Session != "" {
		return nil, mustResolve(d, cs, p.Session)
	}

	active, _ := out["active"].(string)
	if st := theme.ReadOmarchyStatus(); st.Installed || active == theme.OmarchyID {
		out["omarchy"] = map[string]any{
			"installed": st.Installed,
			"dir":       st.Dir,
			"theme":     st.Theme,
			"followed":  st.Installed && (active == "" || active == theme.OmarchyID),
		}
	}

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
					". Write <id>.json there to add it.",
			})
		}
		out["palette"] = pal
	}

	return out, nil
}
