package session

import (
	"encoding/json"
	"strings"
)

// linkChipLabelMax caps the text a link chip shows. The chip is one line of
// chrome pinned to a pane's frame, and a label that outgrew the pane would
// have to be cut by whoever draws it, which moves the cut around with the
// pane's width.
const linkChipLabelMax = 40

// verbPaintLink paints or clears a pane's link chip. The chip is daemon-owned
// state on the window, so painting it is a state mutation and reaches every
// attached client through the ordinary state sync.
func (d *Daemon) verbPaintLink(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Target  string `json:"target"`
		Label   string `json:"label"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}

	label := strings.TrimSpace(p.Label)
	var chip *LinkChip
	if label != "" {
		if p.Target == "" {
			return nil, invalidParam("target", "target is required: the pane the chip jumps to")
		}
		if !sess.hasWindowID(p.Target) {
			return nil, invalidParam("target", "no pane "+echoName(p.Target)+" in session "+echoName(sess.Name()))
		}
		chip = &LinkChip{Label: clampLinkChipLabel(label), Target: p.Target}
	}

	var windowID string
	var cleared bool
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		st.Windows[idx].LinkChip = chip
		windowID = st.Windows[idx].ID
		cleared = chip == nil
		return nil
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	return map[string]any{
		"window_id": windowID,
		"label":     label,
		"cleared":   cleared,
	}, nil
}

// clampLinkChipLabel cuts a label to linkChipLabelMax runes.
func clampLinkChipLabel(s string) string {
	n := 0
	for i := range s {
		if n == linkChipLabelMax {
			return s[:i]
		}
		n++
	}
	return s
}
