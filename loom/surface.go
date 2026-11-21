package loom

import (
	"errors"
	"image"
	"image/color"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/plot"
	"tuios.dev/tml"
)

// Options configure a Surface. The zero value draws at truecolor with the
// default palette on the panel ground.
type Options struct {
	// Palette gives every colour, at the depth of the client. Nil means
	// DefaultPalette(TrueColor).
	Palette *Palette
	// Charts picks the chart glyphs. Nil means braille, or ASCII when the
	// palette's depth is ASCII.
	Charts *plot.Mode
	// Ground is the role under the whole surface. Empty means panel.
	// GroundNone paints no ground: the surface sits on the host's own,
	// such as a terminal pane's background.
	Ground string
	// Limits bound the document and its patches. Nil means
	// tml.DefaultLimits.
	Limits *tml.Limits
	// Now is the clock of ago(). Nil means time.Now.
	Now func() time.Time
	// CellAspect is a cell's height over its width on the client, such as
	// 19.0/8. A pie uses it to stay round. 0 means 2.2.
	CellAspect float64
	// Hyperlinks writes a link's target into its cells as an OSC 8 link.
	// Off by default: a click on a link comes back as a "link" Effect, so
	// the host can ask the person first, as TML requires.
	Hyperlinks bool
	// Placement says where the host shows the surface, such as "popup" or
	// "pane". A binding reads it as view.placement, so a document can show
	// the keys that work where it is, such as "esc close" in a popup only.
	// SetPlacement changes it.
	Placement string
	// Motion plays the enter and exit effects and the skeleton's shimmer.
	// Off, the default, draws each box as it is at once. A host that turns
	// it on asks NextFrame after each Render when to draw again.
	// SetMotion changes it.
	Motion bool
}

// Surface is one TML document drawn into a cell buffer: the tree, the state
// store, the view state of one client, and the retained tree of boxes.
//
// A Surface keeps its boxes between frames. A patch to the state marks the
// boxes whose bindings read the changed paths; the next Render evaluates
// only those, lays the tree out again, and repaints only the boxes whose
// content or place changed. Damage reports what was repainted.
//
// A Surface is not safe for use by several goroutines.
type Surface struct {
	doc   *tml.Node
	store *tml.Store
	view  *View
	deps  *depGraph
	opts  Options
	pal   *Palette
	g     *glyphs
	mode  plot.Mode

	cache    map[*tml.Node]*compiled
	sketches *sketchCache
	hooks    Hooks

	root  *box
	byPtr map[string][]*box
	buf   *uv.Buffer
	out   *Buffer
	w, h  int

	full    bool
	clock   bool
	links   map[image.Point]string
	pending []string
	viewDue []string
	damage  []Rect

	budget     *tml.Budget
	exp        *expansion
	focus      []*box
	gen        uint64
	boxSteps   int
	drag       *box
	focusMoved bool
	litFocus   string
	relayout   bool
	reveal     bool

	stats renderStats

	motions      map[string]*motion
	ghosts       []ghost
	now          time.Time
	animate      bool
	repaint      bool
	resized      bool
	fxCells      []uv.Cell
	mixes        map[mixKey]color.Color
	lastMix      lastMix
	fxBase, fxFg color.Color
}

type renderStats struct {
	built    int
	steps    int
	cells    int
	measures int
}

type compiled struct {
	tmpl    map[string]*tml.Template
	exprs   map[string]*tml.Expr
	actions map[string][]tml.Action
}

// ErrNoDocument is returned for a nil document.
var ErrNoDocument = errors.New("loom: no document")

// New returns a surface for a document. The document should have passed
// tml.Check; loom draws any tree without panicking, but draws only what it
// knows. The store is made with tml.StoreFor, so a patch cannot grow each,
// list or the rows past the limits.
func New(doc *tml.Node, opts *Options) (*Surface, error) {
	if doc == nil {
		return nil, ErrNoDocument
	}
	s := &Surface{view: newView(), full: true}
	if opts != nil {
		s.opts = *opts
	}
	if err := s.setDoc(doc, nil); err != nil {
		return nil, err
	}
	s.SetPalette(s.opts.Palette)
	return s, nil
}

func (s *Surface) setDoc(doc *tml.Node, store *tml.Store) error {
	if store == nil {
		st, err := tml.StoreFor(doc, s.opts.Limits)
		if err != nil {
			return err
		}
		store = st
	}
	s.doc, s.store = doc, store
	s.deps = buildDeps(doc)
	s.boxSteps = shareFor(maxBoxes(doc, s.opts.Limits))
	s.cache = map[*tml.Node]*compiled{}
	s.full = true
	return nil
}

// SetPalette changes the palette, for a new depth or a new theme. The next
// Render repaints everything.
func (s *Surface) SetPalette(p *Palette) {
	if p == nil {
		p = DefaultPalette(TrueColor)
	}
	s.pal = p
	s.g = &unicodeGlyphs
	s.mode = plot.Braille
	if p.Depth == ASCII {
		s.g = &asciiGlyphs
		s.mode = plot.ASCII
	}
	if s.opts.Charts != nil && p.Depth != ASCII {
		s.mode = *s.opts.Charts
	}
	s.full = true
}

// Document returns a copy of the tree. Change the tree with Replace.
func (s *Surface) Document() *tml.Node { return s.doc.Clone() }

// State returns a copy of the state. Change the state through Patch or
// Set, so the surface knows what to redraw.
func (s *Surface) State() map[string]any {
	out, _ := deepCopy(s.store.State()).(map[string]any)
	return out
}

// Version counts the changes to the state.
func (s *Surface) Version() uint64 { return s.store.Version() }

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = deepCopy(e)
		}
		return a
	}
	return v
}

// View returns a copy of this client's view state: the open tabs, sorts,
// filters, folds, focus, cursors, scroll offsets and split ratios. A host
// may keep it, for example across a reconnect; the surface changes it
// only through input.
func (s *Surface) View() *View { return cloneView(s.view) }

// SetView replaces this client's view state with a copy of v, as View
// returned it: for example the view another client of the same surface
// reported, to draw what that client shows. A key that names no node does
// nothing. Nil clears the view. The next Render builds the tree again.
func (s *Surface) SetView(v *View) {
	nv := newView()
	if v != nil {
		maps.Copy(nv.Tabs, v.Tabs)
		maps.Copy(nv.Sort, v.Sort)
		maps.Copy(nv.Filter, v.Filter)
		maps.Copy(nv.Fold, v.Fold)
		maps.Copy(nv.Cursor, v.Cursor)
		maps.Copy(nv.Scroll, v.Scroll)
		maps.Copy(nv.Split, v.Split)
		maps.Copy(nv.Text, v.Text)
		maps.Copy(nv.Caret, v.Caret)
		nv.Focus, nv.Page, nv.PageAnchor, nv.PageRow = v.Focus, v.Page, v.PageAnchor, v.PageRow
	}
	s.view = nv
	s.full = true
}

func cloneView(v *View) *View {
	return &View{Tabs: maps.Clone(v.Tabs), Sort: maps.Clone(v.Sort), Filter: maps.Clone(v.Filter), Fold: maps.Clone(v.Fold),
		Focus: v.Focus, Cursor: maps.Clone(v.Cursor), Scroll: maps.Clone(v.Scroll), Split: maps.Clone(v.Split),
		Text: maps.Clone(v.Text), Caret: maps.Clone(v.Caret), Page: v.Page,
		PageAnchor: v.PageAnchor, PageRow: v.PageRow}
}

// Patch applies a JSON Patch from the owner to the state. Only the boxes
// that read the changed paths are evaluated again at the next Render.
func (s *Surface) Patch(ops []tml.Op) error {
	changed, err := s.store.Apply(ops)
	if err != nil {
		return err
	}
	s.addPending(changed)
	return nil
}

func (s *Surface) addPending(changed []string) {
	for _, c := range changed {
		if !slices.Contains(s.pending, c) {
			s.pending = append(s.pending, c)
		}
	}
}

func (s *Surface) flush() {
	if s.root != nil && (s.full || len(s.pending) > 0 || len(s.viewDue) > 0 || s.clock || s.relayout) {
		s.Render(s.w, s.h)
	}
}

// Set writes one state path, such as state.cpu, as the set action does.
func (s *Surface) Set(path string, value any) error {
	changed, err := s.store.Set(path, value)
	if err != nil {
		return err
	}
	s.addPending(changed)
	return nil
}

// Replace swaps the tree. With keepState the store stays; otherwise the
// state comes from the new document. View state stays, so a table keeps its
// cursor row across a replace when its id does.
func (s *Surface) Replace(doc *tml.Node, keepState bool) error {
	if doc == nil {
		return ErrNoDocument
	}
	var store *tml.Store
	if keepState {
		st, err := tml.NewStore(s.store.State(), s.opts.Limits)
		if err != nil {
			return err
		}
		store = st
	}
	return s.setDoc(doc, store)
}

// SetPlacement changes where the host shows the surface, which a binding
// reads as view.placement. A change makes the next Render build the tree
// again; the same placement does nothing.
func (s *Surface) SetPlacement(p string) {
	if p == s.opts.Placement {
		return
	}
	s.opts.Placement = p
	s.full = true
}

// NeedsClock reports whether a binding calls ago(). Then the host calls
// Tick about once a minute while the surface shows; otherwise the surface
// needs no timer at all.
func (s *Surface) NeedsClock() bool { return len(s.deps.Clock()) > 0 }

// Tick makes the next Render evaluate again only the boxes that call
// ago().
func (s *Surface) Tick() { s.clock = true }

// Invalidate makes the next Render rebuild and repaint everything.
func (s *Surface) Invalidate() { s.full = true }

// Damage returns the rectangles repainted since the last call, and
// forgets them. Renders that Key and Mouse run to apply pending changes
// add to it too, so a host that copies only damaged cells misses nothing.
// The slice is the caller's; no later Render reuses it.
func (s *Surface) Damage() []Rect {
	out := s.damage
	s.damage = nil
	return out
}

func (s *Surface) addDamage(r Rect) {
	if r.Empty() {
		return
	}
	if len(s.damage) >= 256 {
		u := r
		for _, d := range s.damage {
			u = u.Union(d)
		}
		s.damage = append(s.damage[:0], u)
		return
	}
	s.damage = append(s.damage, r)
}

const maxSide = 4096

// Render draws the surface into a w by h buffer and returns it. The buffer
// belongs to the surface and is reused by the next Render. A call with
// nothing changed repaints nothing. A side past 4096 cells counts as 4096.
func (s *Surface) Render(w, h int) *Buffer {
	w, h = max(0, min(w, maxSide)), max(0, min(h, maxSide))
	s.stats = renderStats{}
	s.resized = false
	if s.buf == nil || w != s.w || h != s.h {
		s.buf = uv.NewBuffer(w, h)
		s.out = &Buffer{b: s.buf}
		s.w, s.h = w, h
		s.full = true
		s.resized = true
	}
	s.budget = tml.NewBudget()
	if len(s.motions) > 0 || len(s.ghosts) > 0 {
		s.beginMotion()
	} else {
		s.now = time.Time{}
	}
	if s.full || s.root == nil {
		s.rebuildAll()
	} else if !s.rebuildDirty() && !s.relayout && !s.animate && !s.repaint {
		return s.out
	}
	s.relayout = false
	if len(s.motions) > 0 && s.stats.built > 0 {
		s.departures()
	}

	s.layoutRoot()
	s.focusMoved, s.reveal = false, false
	s.markFocusCards()
	if s.links == nil || s.full {
		s.links = map[image.Point]string{}
	}
	c := &canvas{buf: s.buf, clip: rect(0, 0, w, h), pal: s.pal, g: s.g, cells: &s.stats.cells, links: s.links, osc8: s.opts.Hyperlinks}
	if s.full || s.repaint {
		c.fill(c.clip, s.ground())
		s.addDamage(c.clip)
		s.paint(s.root, c, true)
	} else {
		s.paint(s.root, c, false)
	}
	if len(s.ghosts) > 0 {
		s.drawGhosts()
	}
	if len(s.motions) > 0 {
		s.endMotion()
	}
	s.full, s.repaint = false, false
	return s.out
}

// GroundNone is the Ground that paints none. It names no role, so its
// colour is always nil.
const GroundNone = "none"

func (s *Surface) ground() string {
	if s.opts.Ground != "" {
		return s.opts.Ground
	}
	return "panel"
}

func (s *Surface) rebuildAll() {
	s.pending, s.viewDue = nil, nil
	s.byPtr = map[string][]*box{}
	s.exp = newExpansion(s.opts.Limits)
	root := &box{kind: "tml", src: s.doc, key: "", dirty: true}
	if s.doc.Type == "tml" {
		root.kids = s.buildChildren(s.doc, "", nil, root)
		root.w = &pageWidget{}
	} else {
		root.kids = s.build(s.doc, "", nil, root)
	}
	root.align = "stretch"
	s.root = root
}

func (s *Surface) rebuildDirty() bool {
	if len(s.pending) == 0 && len(s.viewDue) == 0 && !s.clock {
		return false
	}
	var targets []*box
	ptrs := s.deps.Affected(s.pending)
	if s.clock {
		ptrs = append(ptrs, s.deps.Clock()...)
		s.clock = false
	}
	for _, ptr := range ptrs {
		boxes := s.byPtr[ptr]
		for p := ptr; len(boxes) == 0 && p != ""; {
			p = parentPointer(p)
			boxes = s.byPtr[p]
			if p == "" {
				boxes = []*box{s.root}
			}
		}
		for _, b := range boxes {
			if f := formOf(b); f != nil {
				targets = append(targets, f)
				continue
			}
			if _, each := b.src.Attr("each"); each || b.src.AttrString("if") != "" {
				if b.parent != nil {
					b = b.parent
				}
			}
			targets = append(targets, b)
		}
	}
	for _, key := range s.viewDue {
		for _, bs := range s.byPtr {
			for _, b := range bs {
				if b.key == key {
					targets = append(targets, b)
				}
			}
		}
	}
	s.pending, s.viewDue = nil, nil
	if len(targets) == 0 {
		return false
	}
	for i, b := range targets {
		for b.kind == listItem && b.parent != nil {
			b = b.parent
		}
		targets[i] = b
	}
	s.exp = newExpansion(s.opts.Limits)
	done := map[*box]bool{}
	for _, b := range targets {
		if done[b] || insideAny(b, done) || !held(b) {
			continue
		}
		s.rebuild(b)
		done[b] = true
	}
	return true
}

func held(b *box) bool {
	for ; b.parent != nil; b = b.parent {
		if !slices.Contains(b.parent.kids, b) {
			return false
		}
	}
	return true
}

func formOf(b *box) *box {
	for p := b.parent; p != nil; p = p.parent {
		if p.kind == "form" {
			return p
		}
	}
	return nil
}

func insideAny(b *box, set map[*box]bool) bool {
	for p := b.parent; p != nil; p = p.parent {
		if set[p] {
			return true
		}
	}
	return false
}

func (s *Surface) rebuild(b *box) {
	if b.kind == listItem {
		s.rebuild(b.parent)
		return
	}
	if b == s.root {
		s.rebuildAll()
		s.full = true
		return
	}
	if !held(b) {
		return
	}
	s.forget(b)
	nb := s.buildOne(b.src, b.ptr, b.scope, b.parent)
	if nb == nil {
		s.rebuild(b.parent)
		return
	}
	nb.rect, nb.prev = b.rect, b.prev
	nb.dirty = true
	for i, k := range b.parent.kids {
		if k == b {
			b.parent.kids[i] = nb
		}
	}
}

func (s *Surface) forget(b *box) {
	list := s.byPtr[b.ptr]
	for i, x := range list {
		if x == b {
			s.byPtr[b.ptr] = append(list[:i], list[i+1:]...)
			break
		}
	}
	for _, k := range b.kids {
		s.forget(k)
	}
}

func (s *Surface) markView(key string) {
	s.viewDue = append(s.viewDue, key)
}

func (s *Surface) vars(sc *scope) map[string]any {
	m := map[string]any{
		"state": s.store.State(),
		"view":  map[string]any{"width": float64(s.w), "height": float64(s.h), "band": band(s.w), "placement": s.opts.Placement},
	}
	sc.vars(m)
	return m
}

func band(w int) string {
	switch {
	case w < 72:
		return "narrow"
	case w < 120:
		return "medium"
	}
	return "wide"
}

func (s *Surface) env(vars map[string]any) *tml.Env {
	return &tml.Env{Vars: vars, Budget: s.budget, Now: s.opts.Now}
}

func (s *Surface) compiledOf(n *tml.Node) *compiled {
	c := s.cache[n]
	if c == nil {
		c = &compiled{tmpl: map[string]*tml.Template{}, exprs: map[string]*tml.Expr{}, actions: map[string][]tml.Action{}}
		s.cache[n] = c
	}
	return c
}

func (s *Surface) template(n *tml.Node, key, src string) *tml.Template {
	c := s.compiledOf(n)
	t, ok := c.tmpl[key]
	if !ok {
		t, _ = tml.ParseTemplate(src)
		c.tmpl[key] = t
	}
	return t
}

func (s *Surface) expr(n *tml.Node, key, src string) *tml.Expr {
	c := s.compiledOf(n)
	e, ok := c.exprs[key]
	if !ok {
		e, _ = tml.ParseBinding(src)
		c.exprs[key] = e
	}
	return e
}

func (s *Surface) actionsOf(n *tml.Node, attr string) []tml.Action {
	c := s.compiledOf(n)
	a, ok := c.actions[attr]
	if !ok {
		a, _ = tml.ParseActions(n.AttrString(attr))
		c.actions[attr] = a
	}
	return a
}

func (s *Surface) value(n *tml.Node, attr string, vars map[string]any) (any, bool) {
	v, ok := n.Attr(attr)
	if !ok {
		return nil, false
	}
	switch v.Kind {
	case tml.KindString:
		t := s.template(n, attr, v.Str)
		if t == nil {
			return "!", true
		}
		if !t.HasExpr() {
			return v.Str, true
		}
		out, err := t.Eval(s.env(vars))
		if err != nil {
			return "!", true
		}
		return out, true
	case tml.KindNumber:
		f, _ := v.Float()
		return f, true
	case tml.KindBool:
		return v.Bool, true
	}
	d, err := v.Decode()
	if err != nil {
		return nil, true
	}
	return tml.SanitizeValue(d), true
}

func (s *Surface) str(n *tml.Node, attr string, vars map[string]any) string {
	v, ok := s.value(n, attr, vars)
	if !ok || v == nil {
		return ""
	}
	return tml.Stringify(v)
}

func (s *Surface) num(n *tml.Node, attr string, vars map[string]any, def float64) float64 {
	v, ok := s.value(n, attr, vars)
	if !ok {
		return def
	}
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f
		}
	case bool:
		if x {
			return 1
		}
		return 0
	}
	return def
}

func (s *Surface) count(n *tml.Node, attr string, vars map[string]any, def, lo, hi int) int {
	f := s.num(n, attr, vars, float64(def))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return def
	}
	return int(math.Max(float64(lo), math.Min(float64(hi), math.Trunc(f))))
}

func (s *Surface) flag(n *tml.Node, attr string, vars map[string]any, def bool) bool {
	v, ok := s.value(n, attr, vars)
	if !ok {
		return def
	}
	if str, isStr := v.(string); isStr {
		return str == "true"
	}
	return tml.Truthy(v)
}

func (s *Surface) data(n *tml.Node, attr string, vars map[string]any) []any {
	v, ok := n.Attr(attr)
	if !ok {
		return nil
	}
	if v.Kind == tml.KindArray {
		d, err := v.Decode()
		if err != nil {
			return nil
		}
		arr, _ := tml.SanitizeValue(d).([]any)
		return arr
	}
	if v.Kind != tml.KindString {
		return nil
	}
	if root, ptr, err := tml.SplitPath(v.Str); err == nil {
		var base any
		switch {
		case root == "state":
			base = s.store.State()
		default:
			b, ok := vars[root]
			if !ok {
				return nil
			}
			base = b
		}
		arr, _ := walkPointer(base, ptr).([]any)
		return arr
	}
	e := s.expr(n, "data:"+attr, v.Str)
	if e == nil {
		return nil
	}
	out, err := e.Eval(s.env(vars))
	if err != nil {
		return nil
	}
	arr, _ := out.([]any)
	return arr
}

func (s *Surface) truthy(n *tml.Node, attr string, vars map[string]any) bool {
	v, ok := n.Attr(attr)
	if !ok {
		return true
	}
	if v.Kind == tml.KindBool {
		return v.Bool
	}
	e := s.expr(n, attr, v.Text())
	if e == nil {
		return false
	}
	out, err := e.Eval(s.env(vars))
	if err != nil {
		return false
	}
	return tml.Truthy(out)
}

func walkPointer(v any, ptr string) any {
	if ptr == "" {
		return v
	}
	for tok := range strings.SplitSeq(ptr[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			v = x[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}
