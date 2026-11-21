package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/adrg/xdg"
	"github.com/charmbracelet/x/ansi"
)

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
	// two-cell buttons the title bar's hit rectangles are measured
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
	Focus  string `json:"focus,omitempty"`  // "you are here"
	Marker string `json:"marker,omitempty"` // "you are here", the marker row style

	// The icons in front of the rail's section headers, under the icon
	// header style, one cell each.
	SectionSessions  string `json:"section_sessions,omitempty"`
	SectionTerminals string `json:"section_terminals,omitempty"`
	SectionAgents    string `json:"section_agents,omitempty"`
	SectionFiles     string `json:"section_files,omitempty"`
	SectionGit       string `json:"section_git,omitempty"`
	Attention        string `json:"attention,omitempty"` // "this one wants a human"
	Bullet           string `json:"bullet,omitempty"`    // a resting row
	Add              string `json:"add,omitempty"`       // the new-thing control
	Collapse         string `json:"collapse,omitempty"`
	Expand           string `json:"expand,omitempty"`

	// The rail's two tree marks, three cells each. They run in front of a row
	// that belongs to a group above it: TreeBranch while siblings follow,
	// TreeLast on the one that closes the group.
	TreeBranch string `json:"tree_branch,omitempty"`
	TreeLast   string `json:"tree_last,omitempty"`

	// The rail's fold marks, one cell each, on a group header: FoldOpen while
	// the group's rows are on screen under it, FoldShut while it is folded.
	FoldOpen string `json:"fold_open,omitempty"`
	FoldShut string `json:"fold_shut,omitempty"`

	// The files section's three marks, one cell each. They are roles rather
	// than a table of file types on purpose: a set says what a folder, a parent
	// and a plain file look like, and the per-type icons are a separate,
	// nerd-font-only layer that falls back on exactly these three. A set that
	// can be drawn in seven bits therefore keeps a files section that can be,
	// which a table of private-use codepoints could never promise.
	Folder string `json:"folder,omitempty"`
	Parent string `json:"parent,omitempty"` // the ".." row
	File   string `json:"file,omitempty"`

	// The agent-state marks, one per state, drawn wherever a pane's agent
	// state shows: the rail and its strip, the title bars, the palette, the
	// switcher, the Inbox, the cockpit and the dock. One or two cells each,
	// so a mark may be a wide emoji; every surface lays its rows out against
	// the widest mark the set draws. Waiting is display-only: an agent at
	// rest whose subagents still run.
	StateWorking    string `json:"state_working,omitempty"`
	StateNeedsInput string `json:"state_needs_input,omitempty"`
	StateIdle       string `json:"state_idle,omitempty"`
	StateDone       string `json:"state_done,omitempty"`
	StateErrored    string `json:"state_errored,omitempty"`
	StateUnknown    string `json:"state_unknown,omitempty"`
	StateWaiting    string `json:"state_waiting,omitempty"`

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

var builtinGlyphSets = map[string]*GlyphSet{
	GlyphSetNone: {
		ID:          GlyphSetNone,
		DisplayName: "Default",
	},
	"unicode": {
		ID: "unicode", DisplayName: "Unicode only",
		Close: "×", Maximize: "□", Minimize: "−", Dot: "●",
		PillLeft: "▏", PillRight: "▕",
		Rule: "─", ArrowLeft: "‹", ArrowRight: "›",
		Focus: "▎", Attention: "▎", Bullet: "·", Marker: "›",
		SectionSessions: "◆", SectionTerminals: "▸", SectionAgents: "●",
		SectionFiles: "▤", SectionGit: "±",
		Add: "+", Collapse: "«", Expand: "»",
		TreeBranch: "├─ ", TreeLast: "└─ ",
		FoldOpen: "▾", FoldShut: "▸",
		Folder: "▸", Parent: "▴", File: "·",
		Ellipsis: "…", Sigil: "›", DashRule: "╌",
	},
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
		Bullet: "•", ScrollbarThumb: "█", ScrollbarTrack: "│",
	},
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
		Focus: ">", Attention: "!", Bullet: ".", Marker: ">",
		Add: "+", Collapse: "<<", Expand: ">>",
		TreeBranch: "|- ", TreeLast: "`- ",
		FoldOpen: "v", FoldShut: ">",
		Folder: ">", Parent: "^", File: ".",
		ScrollbarThumb: "|", ScrollbarTrack: ".",
		Ellipsis: "...", Sigil: ">", DashRule: "-",
		StateWorking: "*", StateNeedsInput: "!", StateIdle: "o", StateDone: "#",
		StateErrored: "x", StateUnknown: "?", StateWaiting: "%",
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
		return "", errfmt.Errorf("failed to get glyphs directory: %w", err)
	}
	return filepath.Dir(keepFile), nil
}

var (
	glyphMu            sync.RWMutex
	userGlyphSets      = map[string]*GlyphSet{}
	glyphProblems      []string
	glyphProblemsBySet = map[string][]string{}
	activeGlyphID      = GlyphSetNone
	activeGlyphs       atomic.Pointer[GlyphSet]
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
	bySet := map[string][]string{}
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
		if len(probs) > 0 {
			bySet[set.ID] = probs
		}
		sets[set.ID] = set
		loaded = append(loaded, set.ID)
	}
	sort.Strings(loaded)
	glyphMu.Lock()
	userGlyphSets, glyphProblems, glyphProblemsBySet = sets, problems, bySet
	glyphMu.Unlock()
	refreshActiveGlyphs()
	return loaded, problems
}

func loadGlyphSetFile(path string) (*GlyphSet, []string, error) {
	// #nosec G304 - path comes from the user's own config directory
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, errfmt.Errorf("failed to read glyph set: %w", err)
	}
	var set GlyphSet
	if err := jsonx.Unmarshal(data, &set); err != nil {
		return nil, nil, errfmt.Errorf("failed to parse glyph set JSON: %w", err)
	}
	if set.ID == "" {
		base := filepath.Base(path)
		set.ID = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if set.ID == "" {
		return nil, nil, errfmt.Errorf("glyph set has no id")
	}
	if set.DisplayName == "" {
		set.DisplayName = set.ID
	}
	problems := sanitizeGlyphSet(&set)
	return &set, problems, nil
}

type glyphRole struct {
	name  string
	field func(*GlyphSet) *string
	width int
}

const markWidth = -1

// MarkMaxWidth is the widest an agent-state mark may measure.
const MarkMaxWidth = 2

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
	{"marker", func(g *GlyphSet) *string { return &g.Marker }, 1},
	{"section_sessions", func(g *GlyphSet) *string { return &g.SectionSessions }, 1},
	{"section_terminals", func(g *GlyphSet) *string { return &g.SectionTerminals }, 1},
	{"section_agents", func(g *GlyphSet) *string { return &g.SectionAgents }, 1},
	{"section_files", func(g *GlyphSet) *string { return &g.SectionFiles }, 1},
	{"section_git", func(g *GlyphSet) *string { return &g.SectionGit }, 1},
	{"attention", func(g *GlyphSet) *string { return &g.Attention }, 1},
	{"bullet", func(g *GlyphSet) *string { return &g.Bullet }, 1},
	{"add", func(g *GlyphSet) *string { return &g.Add }, 1},
	{"tree_branch", func(g *GlyphSet) *string { return &g.TreeBranch }, 3},
	{"tree_last", func(g *GlyphSet) *string { return &g.TreeLast }, 3},
	{"fold_open", func(g *GlyphSet) *string { return &g.FoldOpen }, 1},
	{"fold_shut", func(g *GlyphSet) *string { return &g.FoldShut }, 1},
	{"folder", func(g *GlyphSet) *string { return &g.Folder }, 1},
	{"parent", func(g *GlyphSet) *string { return &g.Parent }, 1},
	{"file", func(g *GlyphSet) *string { return &g.File }, 1},
	{"collapse", func(g *GlyphSet) *string { return &g.Collapse }, 0},
	{"expand", func(g *GlyphSet) *string { return &g.Expand }, 0},
	{"state_working", func(g *GlyphSet) *string { return &g.StateWorking }, markWidth},
	{"state_needs_input", func(g *GlyphSet) *string { return &g.StateNeedsInput }, markWidth},
	{"state_idle", func(g *GlyphSet) *string { return &g.StateIdle }, markWidth},
	{"state_done", func(g *GlyphSet) *string { return &g.StateDone }, markWidth},
	{"state_errored", func(g *GlyphSet) *string { return &g.StateErrored }, markWidth},
	{"state_unknown", func(g *GlyphSet) *string { return &g.StateUnknown }, markWidth},
	{"state_waiting", func(g *GlyphSet) *string { return &g.StateWaiting }, markWidth},
	{"scrollbar_thumb", func(g *GlyphSet) *string { return &g.ScrollbarThumb }, 1},
	{"scrollbar_track", func(g *GlyphSet) *string { return &g.ScrollbarTrack }, 1},
	{"ellipsis", func(g *GlyphSet) *string { return &g.Ellipsis }, 0},
	{"sigil", func(g *GlyphSet) *string { return &g.Sigil }, 1},
	{"dash_rule", func(g *GlyphSet) *string { return &g.DashRule }, 1},
}

func borderFields(b *BorderGlyphs) []*string {
	return []*string{
		&b.Top, &b.Bottom, &b.Left, &b.Right,
		&b.TopLeft, &b.TopRight, &b.BottomLeft, &b.BottomRight,
		&b.Middle, &b.MiddleTop, &b.MiddleBottom, &b.MiddleLeft, &b.MiddleRight,
	}
}

func sanitizeGlyphSet(set *GlyphSet) []string {
	var problems []string
	for _, role := range glyphRoles {
		p := role.field(set)
		if *p == "" {
			continue
		}
		if !glyphPrintable(*p) {
			problems = append(problems, fmt.Sprintf(
				"glyph set %s: %s holds a control character or a byte that is not UTF-8, so it keeps the default",
				set.ID, role.name))
			*p = ""
			continue
		}
		if role.width == markWidth {
			if w, ok := MarkWidth(*p); !ok {
				problems = append(problems, fmt.Sprintf(
					"glyph set %s: %s is %d cells wide and the layout budgets 1 or 2, so it keeps the default",
					set.ID, role.name, w))
				*p = ""
			}
			continue
		}
		if role.width == 0 {
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
			if *p != "" && (!glyphPrintable(*p) || lipgloss.Width(*p) != 1) {
				problems = append(problems, fmt.Sprintf(
					"glyph set %s: a border rune must be one cell, so %q keeps the default", set.ID, *p))
				*p = ""
			}
		}
	}
	set.ASCII = glyphSetIsASCII(set)
	return problems
}

func glyphPrintable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// MarkWidth measures an agent-state mark and reports whether it can be drawn:
// one or two cells, and the same width whether it is measured as one grapheme
// or rune by rune.
//
// The second test is what refuses an emoji that is wide only because of what
// follows it. "⚠" is one cell and "⚠️" (with the emoji variation selector),
// a skin tone or a joined family measure two as a grapheme, but terminals
// disagree about that, and a terminal that draws one cell would shift every
// cell after the mark. A mark that is wide by itself, "✋" or "⛔", is two
// cells everywhere.
func MarkWidth(s string) (int, bool) {
	w := ansi.StringWidth(s)
	return w, w >= 1 && w <= MarkMaxWidth && w == ansi.StringWidthWc(s) && glyphPrintable(s)
}

// StateMark is the set's mark for one agent state, by the state's wire name,
// or the empty string when the set leaves it to the default.
func (g *GlyphSet) StateMark(state string) string {
	switch state {
	case "working":
		return g.StateWorking
	case "needs_input":
		return g.StateNeedsInput
	case "idle":
		return g.StateIdle
	case "done":
		return g.StateDone
	case "errored":
		return g.StateErrored
	case "unknown":
		return g.StateUnknown
	case "waiting":
		return g.StateWaiting
	}
	return ""
}

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

// GlyphSetProblemsFor returns the lines one set produced when it was read: the
// roles it named and did not get, one per line.
//
// A role whose glyph is the wrong width for its slot is dropped at load, so it
// is not in the set afterwards and cannot be found by comparing the set against
// what draws. Without this the drop is invisible to everything but the log,
// which is where "my glyph set is not working" comes from.
func GlyphSetProblemsFor(id string) []string {
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	return append([]string(nil), glyphProblemsBySet[id]...)
}

// GlyphSetDroppedRoles is the roles one set named and did not get, by name.
// The lines from GlyphSetProblemsFor say why in a sentence; this is what to put
// where only the names fit.
func GlyphSetDroppedRoles(id string) []string {
	var roles []string
	for _, line := range GlyphSetProblemsFor(id) {
		_, rest, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		if strings.HasPrefix(rest, "a border rune") {
			roles = append(roles, "border")
			continue
		}
		role, _, ok := strings.Cut(rest, " ")
		if ok && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return slices.Compact(roles)
}

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
		mergeGlyphSet(out, set)
		id = set.Inherits
	}
	out.ASCII = glyphSetIsASCII(out)
	return out
}

func lookupGlyphSet(id string) *GlyphSet {
	glyphMu.RLock()
	set := userGlyphSets[id]
	glyphMu.RUnlock()
	if set != nil {
		return set
	}
	return builtinGlyphSets[id]
}

func mergeGlyphSet(dst, src *GlyphSet) {
	if dst.DisplayName == "" {
		dst.DisplayName = src.DisplayName
	}
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
	if id != GlyphSetNone && lookupGlyphSet(id) == nil {
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
