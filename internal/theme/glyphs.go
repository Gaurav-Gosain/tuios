package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/adrg/xdg"
)

// A glyph set is the other half of a rice. A theme says what colour the chrome
// is; a set says what shape it is - which corner the border turns, what the
// close button is a picture of, what a rule is drawn with, which mark says
// "you are here" on the rail. Every one of those was a literal in the render
// path, so the only rice tuios could be asked for was a recolour.
//
// It is one option rather than twenty-two because twenty-two narrow options is
// how a config file becomes unreadable, and because the glyphs are not
// independent of each other: a border, its junctions and the rule that meets
// them have to be drawn in one weight or the joins do not line up. A set is the
// unit that holds them together, and inheritance is how a user who wants one
// glyph different from a built-in says exactly that.

// BorderGlyphs is a border's thirteen runes, in the shape lipgloss draws from.
// An empty field takes the style appearance.border_style names, so a set that
// only wants different corners says only corners.
type BorderGlyphs struct {
	Top         string `json:"top,omitempty"`
	Bottom      string `json:"bottom,omitempty"`
	Left        string `json:"left,omitempty"`
	Right       string `json:"right,omitempty"`
	TopLeft     string `json:"top_left,omitempty"`
	TopRight    string `json:"top_right,omitempty"`
	BottomLeft  string `json:"bottom_left,omitempty"`
	BottomRight string `json:"bottom_right,omitempty"`
	// The junctions. The shared-border grid derives a divider's crossings and
	// tees from these, so a set that leaves them empty gets crossings drawn
	// from its own strokes rather than from another style's.
	Middle       string `json:"middle,omitempty"`
	MiddleTop    string `json:"middle_top,omitempty"`
	MiddleBottom string `json:"middle_bottom,omitempty"`
	MiddleLeft   string `json:"middle_left,omitempty"`
	MiddleRight  string `json:"middle_right,omitempty"`
}

// GlyphSet is one named set of chrome characters. Every field is optional and
// an empty one keeps whatever the set it inherits from says, falling through in
// the end to the glyph tuios ships.
//
// The roles are named for what they draw rather than for where they are drawn,
// because one role is usually drawn in several places: Rule is the pane's
// hairline and the dock's, Bullet is the rail's resting mark in both the full
// rail and the collapsed strip.
type GlyphSet struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// Inherits names the set this one starts from. Empty starts from the
	// built-in defaults, which is what most sets want.
	Inherits string `json:"inherits,omitempty"`
	// ASCII says every glyph in the resolved set is 7-bit. It is computed on
	// load rather than read from the file, because it is a claim about the set
	// that a user cannot be allowed to get wrong: it is what lets a set be
	// offered to a terminal that cannot draw more.
	ASCII bool `json:"ascii"`

	Border *BorderGlyphs `json:"border,omitempty"`

	// The window controls. One cell each: the renderer pads them into the
	// three- and four-cell buttons the title bar's hit rectangles are measured
	// against, so a set cannot move a button out from under the pointer.
	Close     string `json:"close,omitempty"`
	Maximize  string `json:"maximize,omitempty"`
	Minimize  string `json:"minimize,omitempty"`
	Dot       string `json:"dot,omitempty"`       // the traffic-light disc
	PillLeft  string `json:"pill_left,omitempty"` // the cap opening a pill
	PillRight string `json:"pill_right,omitempty"`

	Rule       string `json:"rule,omitempty"`      // one cell, repeated into a hairline
	Separator  string `json:"separator,omitempty"` // the dock's gap between groups; any width
	ArrowLeft  string `json:"arrow_left,omitempty"`
	ArrowRight string `json:"arrow_right,omitempty"`

	// The rail's marks, one cell each.
	Focus     string `json:"focus,omitempty"`     // "you are here"
	Attention string `json:"attention,omitempty"` // "this one wants a human"
	Bullet    string `json:"bullet,omitempty"`    // a resting row
	Add       string `json:"add,omitempty"`       // the new-thing control
	Collapse  string `json:"collapse,omitempty"`
	Expand    string `json:"expand,omitempty"`

	ScrollbarThumb string `json:"scrollbar_thumb,omitempty"`
	ScrollbarTrack string `json:"scrollbar_track,omitempty"`

	Ellipsis string `json:"ellipsis,omitempty"` // any width
	Sigil    string `json:"sigil,omitempty"`
	DashRule string `json:"dash_rule,omitempty"`
}

// GlyphSetNone is the id meaning "draw what tuios ships". It is the default and
// it is spelled out rather than left as the empty string so that list-glyphs
// has something to name and set-config has something to accept.
const GlyphSetNone = "default"

// builtinGlyphSets are the sets that need no file. Each says only what it
// changes, so the built-in glyphs stay in one place (the config package, where
// the renderer reads them) rather than being copied here to drift.
//
// Four rather than a gallery. These are the shapes a set has to be able to
// take, and they are here to be inherited from and to prove the mechanism on a
// terminal with no config file, not to be a theme store. A gallery belongs in
// the glyphs directory, where it costs nothing to carry.
var builtinGlyphSets = map[string]*GlyphSet{
	GlyphSetNone: {
		ID:          GlyphSetNone,
		DisplayName: "Default",
	},
	// Box-drawing only: nothing from the Nerd Font private use area, so the
	// chrome draws whole on a plain Unicode font. It is the set to reach for
	// when the terminal has a good font that is not a patched one, which ASCII
	// mode serves far more bluntly than it needs to.
	"unicode": {
		ID: "unicode", DisplayName: "Unicode only",
		Close: "×", Maximize: "□", Minimize: "−", Dot: "●",
		PillLeft: "▏", PillRight: "▕",
		Rule: "─", ArrowLeft: "‹", ArrowRight: "›",
		Focus: "▎", Attention: "▎", Bullet: "·",
		Add: "+", Collapse: "«", Expand: "»",
		Ellipsis: "…", Sigil: "›", DashRule: "╌",
	},
	// One weight heavier throughout, border included, for a frame that reads
	// from across a room. The junctions are given explicitly because a heavy
	// stroke meeting a light crossing is the join that looks wrong.
	"heavy": {
		ID: "heavy", DisplayName: "Heavy",
		Border: &BorderGlyphs{
			Top: "━", Bottom: "━", Left: "┃", Right: "┃",
			TopLeft: "┏", TopRight: "┓",
			BottomLeft: "┗", BottomRight: "┛",
			Middle: "╋", MiddleTop: "┳", MiddleBottom: "┻",
			MiddleLeft: "┣", MiddleRight: "┫",
		},
		Rule: "━", Focus: "█", Attention: "█",
		Bullet: "▪", ScrollbarThumb: "█", ScrollbarTrack: "│",
	},
	// Nothing outside 7-bit ASCII, for a terminal or a font that cannot be
	// trusted with more. It is what --ascii-only draws, said as a set so that a
	// user can inherit from it and put back the two glyphs they know their
	// terminal can manage.
	"ascii": {
		ID: "ascii", DisplayName: "ASCII", ASCII: true,
		Border: &BorderGlyphs{
			Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
			Middle: "+", MiddleTop: "+", MiddleBottom: "+",
			MiddleLeft: "+", MiddleRight: "+",
		},
		Close: "X", Maximize: "O", Minimize: "-", Dot: "o",
		PillLeft: "[", PillRight: "]",
		Rule: "-", Separator: " | ", ArrowLeft: "<", ArrowRight: ">",
		Focus: ">", Attention: "!", Bullet: ".",
		Add: "+", Collapse: "<<", Expand: ">>",
		ScrollbarThumb: "|", ScrollbarTrack: ".",
		Ellipsis: "...", Sigil: ">", DashRule: "-",
	},
}

// GetGlyphsDir returns the directory user glyph sets are read from
// (~/.config/tuios/glyphs/), creating it if it is not there. It is the themes
// directory's sibling, and for the same reason: a set is a file the user writes
// and tuios reads, not a section of the config file, because it is a document
// with a shape of its own that people copy between machines.
func GetGlyphsDir() (string, error) {
	keepFile, err := xdg.ConfigFile("tuios/glyphs/.keep")
	if err != nil {
		return "", fmt.Errorf("failed to get glyphs directory: %w", err)
	}
	return filepath.Dir(keepFile), nil
}

var (
	glyphMu       sync.RWMutex
	userGlyphSets = map[string]*GlyphSet{}
	glyphProblems []string
	activeGlyphID = GlyphSetNone
	// activeGlyphs is read once per glyph the renderer draws, so it is an
	// atomic pointer to a set nothing mutates after it is published rather than
	// a value behind the mutex above: a copy of thirty strings per accessor
	// call, on a path that resolves a border's corners one at a time, is not
	// what this should cost.
	activeGlyphs atomic.Pointer[GlyphSet]
)

// ReloadGlyphSets re-reads the glyphs directory and returns the ids it found
// and one line per file it could not use.
//
// Re-read rather than cached for the same reason the themes directory is: the
// caller most likely to ask is an agent that has just written the file, and
// telling it the set it authored does not exist is the one answer that is
// certainly wrong.
func ReloadGlyphSets() (loaded, problems []string) {
	dir, err := GetGlyphsDir()
	if err != nil {
		return nil, []string{err.Error()}
	}
	sets := map[string]*GlyphSet{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{fmt.Sprintf("failed to read glyphs directory: %v", err)}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		set, probs, err := loadGlyphSetFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			problems = append(problems, fmt.Sprintf("skipping glyph set %s: %v", entry.Name(), err))
			continue
		}
		problems = append(problems, probs...)
		sets[set.ID] = set
		loaded = append(loaded, set.ID)
	}
	sort.Strings(loaded)
	glyphMu.Lock()
	userGlyphSets, glyphProblems = sets, problems
	glyphMu.Unlock()
	// A set that has just been rewritten on disk is the one most likely to be
	// the active one, so re-resolve rather than wait for the next set-config.
	refreshActiveGlyphs()
	return loaded, problems
}

// loadGlyphSetFile reads one file, drops the roles it cannot draw, and reports
// each drop as a problem.
func loadGlyphSetFile(path string) (*GlyphSet, []string, error) {
	// #nosec G304 - path comes from the user's own config directory
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read glyph set: %w", err)
	}
	var set GlyphSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, nil, fmt.Errorf("failed to parse glyph set JSON: %w", err)
	}
	if set.ID == "" {
		base := filepath.Base(path)
		set.ID = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if set.ID == "" {
		return nil, nil, fmt.Errorf("glyph set has no id")
	}
	if set.DisplayName == "" {
		set.DisplayName = set.ID
	}
	problems := sanitizeGlyphSet(&set)
	return &set, problems, nil
}

// glyphRole is one settable role and the width the layout budgets for it.
type glyphRole struct {
	name  string
	field func(*GlyphSet) *string
	// width is the cells the glyph must measure, or 0 where any width goes.
	width int
}

// glyphRoles is every role a set can name, with the width each one has to
// measure.
//
// The widths are the invariant this whole feature has to keep. A window
// control's press rectangle is a fixed offset from the border's trailing corner
// (see config's button position constants), the rail budgets exactly one column
// for its gutter mark, and a scrollbar draws one cell per row. A two-cell
// emoji in any of those does not look bold, it moves every cell after it and
// puts the close button under a different column than the one the pointer is
// tested against. So a role with a width is checked and a glyph that misses is
// dropped back to the default with a line saying so, rather than drawn.
var glyphRoles = []glyphRole{
	{"close", func(g *GlyphSet) *string { return &g.Close }, 1},
	{"maximize", func(g *GlyphSet) *string { return &g.Maximize }, 1},
	{"minimize", func(g *GlyphSet) *string { return &g.Minimize }, 1},
	{"dot", func(g *GlyphSet) *string { return &g.Dot }, 1},
	{"pill_left", func(g *GlyphSet) *string { return &g.PillLeft }, 1},
	{"pill_right", func(g *GlyphSet) *string { return &g.PillRight }, 1},
	{"rule", func(g *GlyphSet) *string { return &g.Rule }, 1},
	{"separator", func(g *GlyphSet) *string { return &g.Separator }, 0},
	{"arrow_left", func(g *GlyphSet) *string { return &g.ArrowLeft }, 1},
	{"arrow_right", func(g *GlyphSet) *string { return &g.ArrowRight }, 1},
	{"focus", func(g *GlyphSet) *string { return &g.Focus }, 1},
	{"attention", func(g *GlyphSet) *string { return &g.Attention }, 1},
	{"bullet", func(g *GlyphSet) *string { return &g.Bullet }, 1},
	{"add", func(g *GlyphSet) *string { return &g.Add }, 1},
	// The rail measures its own footer rather than budgeting a column for it,
	// so its stepper takes any width; the ASCII default is two cells.
	{"collapse", func(g *GlyphSet) *string { return &g.Collapse }, 0},
	{"expand", func(g *GlyphSet) *string { return &g.Expand }, 0},
	{"scrollbar_thumb", func(g *GlyphSet) *string { return &g.ScrollbarThumb }, 1},
	{"scrollbar_track", func(g *GlyphSet) *string { return &g.ScrollbarTrack }, 1},
	{"ellipsis", func(g *GlyphSet) *string { return &g.Ellipsis }, 0},
	{"sigil", func(g *GlyphSet) *string { return &g.Sigil }, 1},
	{"dash_rule", func(g *GlyphSet) *string { return &g.DashRule }, 1},
}

// borderFields is the border's runes, for the width check and for the merge.
func borderFields(b *BorderGlyphs) []*string {
	return []*string{
		&b.Top, &b.Bottom, &b.Left, &b.Right,
		&b.TopLeft, &b.TopRight, &b.BottomLeft, &b.BottomRight,
		&b.Middle, &b.MiddleTop, &b.MiddleBottom, &b.MiddleLeft, &b.MiddleRight,
	}
}

// sanitizeGlyphSet drops every role whose glyph cannot be drawn where the
// layout puts it, and records whether what is left is 7-bit throughout.
func sanitizeGlyphSet(set *GlyphSet) []string {
	var problems []string
	for _, role := range glyphRoles {
		p := role.field(set)
		if *p == "" || role.width == 0 {
			continue
		}
		if w := lipgloss.Width(*p); w != role.width {
			problems = append(problems, fmt.Sprintf(
				"glyph set %s: %s is %d cells wide and the layout budgets %d, so it keeps the default",
				set.ID, role.name, w, role.width))
			*p = ""
		}
	}
	if set.Border != nil {
		for _, p := range borderFields(set.Border) {
			if *p != "" && lipgloss.Width(*p) != 1 {
				problems = append(problems, fmt.Sprintf(
					"glyph set %s: a border rune must be one cell, so %q keeps the default", set.ID, *p))
				*p = ""
			}
		}
	}
	set.ASCII = glyphSetIsASCII(set)
	return problems
}

// glyphSetIsASCII reports whether every glyph the set names is 7-bit.
//
// A claim about the set's own fields, not about what would be drawn: a role the
// set leaves alone reads as the empty string here, which is trivially 7-bit
// while the built-in it falls back to may not be. Whether a set can be drawn on
// a terminal that manages nothing else is a question about the resolved glyphs
// and is answered where those are resolved, by the describe verb.
func glyphSetIsASCII(set *GlyphSet) bool {
	for _, role := range glyphRoles {
		if !overlay.IsASCII(*role.field(set)) {
			return false
		}
	}
	if set.Border != nil {
		for _, p := range borderFields(set.Border) {
			if !overlay.IsASCII(*p) {
				return false
			}
		}
	}
	return true
}

// GlyphSetExists reports whether id names a set, re-reading the glyphs
// directory once before it answers no, so that "write the file, then select it"
// is one round trip.
func GlyphSetExists(id string) bool {
	if id == "" || id == GlyphSetNone {
		return true
	}
	glyphMu.RLock()
	_, user := userGlyphSets[id]
	glyphMu.RUnlock()
	if user {
		return true
	}
	if _, ok := builtinGlyphSets[id]; ok {
		return true
	}
	ReloadGlyphSets()
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	_, ok := userGlyphSets[id]
	return ok
}

// AvailableGlyphSets lists every set id, built-ins first and then the user's,
// each group sorted.
func AvailableGlyphSets() []string {
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	var builtin, user []string
	for id := range builtinGlyphSets {
		builtin = append(builtin, id)
	}
	for id := range userGlyphSets {
		if _, shadowed := builtinGlyphSets[id]; !shadowed {
			user = append(user, id)
		}
	}
	sort.Strings(builtin)
	sort.Strings(user)
	return append(builtin, user...)
}

// GlyphSetProblems returns the lines from the last directory read. They are the
// answer to "why is my set not drawing", which is otherwise only a log line
// nobody outside the process can read.
func GlyphSetProblems() []string {
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	return append([]string(nil), glyphProblems...)
}

// maxGlyphInherit caps the inheritance walk. A set inheriting from a set is
// how a user says "the heavy one but with my own close button"; a chain deeper
// than this is a mistake, and a chain that loops is one this has to survive.
const maxGlyphInherit = 8

// ResolveGlyphSet returns the set named, with everything it inherits already
// folded in. A name that resolves to nothing returns the empty set, which draws
// the built-in glyphs.
func ResolveGlyphSet(id string) *GlyphSet {
	out := &GlyphSet{ID: id}
	seen := map[string]bool{}
	for range maxGlyphInherit {
		if id == "" || id == GlyphSetNone || seen[id] {
			break
		}
		seen[id] = true
		set := lookupGlyphSet(id)
		if set == nil {
			break
		}
		// Walking from the most specific outwards and only filling what is
		// still empty means the near set wins without a second pass.
		mergeGlyphSet(out, set)
		id = set.Inherits
	}
	out.ASCII = glyphSetIsASCII(out)
	return out
}

// lookupGlyphSet finds a set by id, preferring the user's file over a built-in
// of the same name so a shipped set can be replaced rather than only extended.
func lookupGlyphSet(id string) *GlyphSet {
	glyphMu.RLock()
	set := userGlyphSets[id]
	glyphMu.RUnlock()
	if set != nil {
		return set
	}
	return builtinGlyphSets[id]
}

// mergeGlyphSet fills dst's empty roles from src, leaving what dst already has.
func mergeGlyphSet(dst, src *GlyphSet) {
	if dst.DisplayName == "" {
		dst.DisplayName = src.DisplayName
	}
	// The nearest set's own Inherits, so a resolved set still says where it
	// started. Without it the chain was walked correctly and reported as empty.
	if dst.Inherits == "" {
		dst.Inherits = src.Inherits
	}
	for _, role := range glyphRoles {
		if d := role.field(dst); *d == "" {
			*d = *role.field(src)
		}
	}
	if src.Border == nil {
		return
	}
	if dst.Border == nil {
		dst.Border = &BorderGlyphs{}
	}
	dstFields, srcFields := borderFields(dst.Border), borderFields(src.Border)
	for i := range dstFields {
		if *dstFields[i] == "" {
			*dstFields[i] = *srcFields[i]
		}
	}
}

// SetActiveGlyphs selects the set the chrome is drawn with. An unknown id
// leaves the built-ins in place rather than blanking the chrome, matching what
// validation warns about at load.
func SetActiveGlyphs(id string) {
	if id == "" {
		id = GlyphSetNone
	}
	glyphMu.Lock()
	activeGlyphID = id
	glyphMu.Unlock()
	// A set that does not resolve yet is the ordinary case at startup, not an
	// error: the glyphs directory is read lazily, so the first thing that names
	// a set from it is usually the config file being applied. Without this a
	// user's own set was recorded, reported as active, and drew the built-in
	// glyphs until something else happened to scan the directory.
	//
	// ReloadGlyphSets ends by refreshing the active set, so this is not a
	// second resolve.
	if id != GlyphSetNone && lookupGlyphSet(id) == nil {
		// ReloadGlyphSets refreshes on its way out, but returns early on a
		// directory it cannot read at all. Refreshing here unconditionally
		// costs one resolve and means the drawn glyphs can never disagree with
		// the id get-config reports.
		ReloadGlyphSets()
	}
	refreshActiveGlyphs()
}

// ActiveGlyphSetID is the id currently selected, whether or not it resolved.
func ActiveGlyphSetID() string {
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	return activeGlyphID
}

// refreshActiveGlyphs re-resolves the selected set and pushes the overlay
// family's share of it, which is the one consumer that cannot read this
// package (it depends on nothing inside tuios so that it can be lifted out).
func refreshActiveGlyphs() {
	glyphMu.RLock()
	id := activeGlyphID
	glyphMu.RUnlock()
	resolved := ResolveGlyphSet(id)
	activeGlyphs.Store(resolved)
	overlay.SetChrome(&overlay.Chrome{
		Ellipsis:   resolved.Ellipsis,
		Sigil:      resolved.Sigil,
		ArrowLeft:  resolved.ArrowLeft,
		ArrowRight: resolved.ArrowRight,
		Rule:       resolved.Rule,
		DashRule:   resolved.DashRule,
	})
}

// emptyGlyphs is what Glyphs answers before anything has selected a set: no
// role named, so every caller draws its own default.
var emptyGlyphs = &GlyphSet{ID: GlyphSetNone}

// Glyphs returns the resolved active set. Every field may be empty, and an
// empty field means the caller should draw its own default.
//
// The returned set is never mutated, so a renderer reading four roles in a row
// reads one consistent set even if another session selects a different one
// mid-frame.
func Glyphs() *GlyphSet {
	if g := activeGlyphs.Load(); g != nil {
		return g
	}
	return emptyGlyphs
}

// The reflection-free introspection the describe verb needs. It is here rather
// than in the verb because the role table is here, and a second table written
// beside the verb would be the copy that goes stale the moment a role is added.

// borderRoleNames names the border's runes, in the order borderFields returns
// them, so the two are read together or not at all.
var borderRoleNames = []string{
	"top", "bottom", "left", "right",
	"top_left", "top_right", "bottom_left", "bottom_right",
	"middle", "middle_top", "middle_bottom", "middle_left", "middle_right",
}

// GlyphRoleNames is every scalar role a set can name.
func GlyphRoleNames() []string {
	names := make([]string, 0, len(glyphRoles))
	for _, r := range glyphRoles {
		names = append(names, r.name)
	}
	return names
}

// GlyphBorderRoleNames is every rune of the border a set can name.
func GlyphBorderRoleNames() []string {
	return append([]string(nil), borderRoleNames...)
}

// GlyphSetRoles is the set's scalar roles by name. A role the set leaves alone
// reads as the empty string.
func GlyphSetRoles(set *GlyphSet) map[string]string {
	out := make(map[string]string, len(glyphRoles))
	for _, r := range glyphRoles {
		out[r.name] = *r.field(set)
	}
	return out
}

// GlyphSetBorderRoles is the border's runes by name.
func GlyphSetBorderRoles(b *BorderGlyphs) map[string]string {
	fields := borderFields(b)
	out := make(map[string]string, len(fields))
	for i, name := range borderRoleNames {
		out[name] = *fields[i]
	}
	return out
}
