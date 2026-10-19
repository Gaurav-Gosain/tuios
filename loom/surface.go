package loom

import (
	"errors"
	"image"
	"math"
	"slices"
	"strconv"
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

	cache map[*tml.Node]*compiled

	root  *box
	byPtr map[string][]*box
	buf   *uv.Buffer
	out   *Buffer
	w, h  int

	full    bool                   // the next Render rebuilds and repaints everything
	clock   bool                   // Tick asked for the ago() readers to evaluate again
	links   map[image.Point]string // the link under each cell
	pending []string               // state pointers changed since the last Render
	viewDue []string               // view keys changed since the last Render
	damage  []Rect

	budget *tml.Budget
	exp    *expansion
	focus  []*box // focusable boxes in tree order, from the last layout
	gen    uint64 // the layout pass, for the measure cache
	drag   *box   // the split whose divider is being dragged

	// The work of the last Render, for the budgets the tests hold.
	stats renderStats
}

// renderStats counts the work of one Render.
type renderStats struct {
	built    int // boxes evaluated
	cells    int // cells written
	measures int // boxes measured (not counting cache hits)
}

// compiled holds the parsed bindings and actions of one source node.
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
	s := &Surface{view: NewView(), full: true}
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

// Document returns the tree.
func (s *Surface) Document() *tml.Node { return s.doc }

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

// View returns the view state of this client.
func (s *Surface) View() *View { return s.view }

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

// addPending records changed state paths for the next Render, once each,
// so a hidden surface that gets many patches pays once per path.
func (s *Surface) addPending(changed []string) {
	for _, c := range changed {
		if !slices.Contains(s.pending, c) {
			s.pending = append(s.pending, c)
		}
	}
}

// flush brings the boxes up to date with pending changes before input
// acts on them, at the last size.
func (s *Surface) flush() {
	if s.root != nil && (s.full || len(s.pending) > 0 || len(s.viewDue) > 0 || s.clock) {
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

// NeedsClock reports whether a binding calls ago(). Then the host calls
// Tick about once a minute while the surface shows; otherwise the surface
// needs no timer at all.
func (s *Surface) NeedsClock() bool { return len(s.deps.Clock()) > 0 }

// Tick makes the next Render evaluate again only the boxes that call
// ago().
func (s *Surface) Tick() { s.clock = true }

// Invalidate makes the next Render rebuild and repaint everything.
func (s *Surface) Invalidate() { s.full = true }

// Damage returns the rectangles the last Render repainted. A host that
// diffs cells needs it only to skip work.
func (s *Surface) Damage() []Rect { return s.damage }

// maxSide bounds the width and height of a surface, so a resize passed on
// unchecked cannot allocate without bound.
const maxSide = 4096

// Render draws the surface into a w by h buffer and returns it. The buffer
// belongs to the surface and is reused by the next Render. A call with
// nothing changed repaints nothing. A side past 4096 cells counts as 4096.
func (s *Surface) Render(w, h int) *Buffer {
	w, h = max(0, min(w, maxSide)), max(0, min(h, maxSide))
	s.damage = s.damage[:0]
	s.stats = renderStats{}
	if s.buf == nil || w != s.w || h != s.h {
		s.buf = uv.NewBuffer(w, h)
		s.out = &Buffer{b: s.buf}
		s.w, s.h = w, h
		s.full = true
	}
	s.budget = tml.NewBudget()
	if s.full || s.root == nil {
		s.rebuildAll()
	} else if !s.rebuildDirty() {
		return s.out
	}
	s.layoutRoot()
	if s.links == nil || s.full {
		s.links = map[image.Point]string{}
	}
	c := &canvas{buf: s.buf, clip: rect(0, 0, w, h), pal: s.pal, g: s.g, cells: &s.stats.cells, links: s.links, osc8: s.opts.Hyperlinks}
	if s.full {
		c.fill(c.clip, s.ground())
		s.damage = append(s.damage, c.clip)
		s.paint(s.root, c, true)
	} else {
		s.paint(s.root, c, false)
	}
	s.full = false
	return s.out
}

func (s *Surface) ground() string {
	if s.opts.Ground != "" {
		return s.opts.Ground
	}
	return "panel"
}

// rebuildAll builds the whole box tree from the document.
func (s *Surface) rebuildAll() {
	s.pending, s.viewDue = nil, nil
	s.byPtr = map[string][]*box{}
	s.exp = newExpansion(s.opts.Limits)
	root := &box{kind: "tml", src: s.doc, key: "", dirty: true}
	if s.doc.Type == "tml" {
		root.kids = s.buildChildren(s.doc, "", nil, root)
	} else {
		// A fragment, such as the one row of a dock cell.
		root.kids = s.build(s.doc, "", nil, root)
	}
	root.align = "stretch"
	s.root = root
}

// rebuildDirty evaluates again the boxes that read changed state or whose
// view state changed. It reports whether anything changed.
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
			// A node with no box (if was false, or a closed modal):
			// build the nearest ancestor that has one.
			p = parentPointer(p)
			boxes = s.byPtr[p]
			if p == "" {
				boxes = []*box{s.root}
			}
		}
		for _, b := range boxes {
			// Inside a form, the field values are the form's scope: the form
			// builds again, so form.NAME readers see the new values.
			if f := formOf(b); f != nil {
				targets = append(targets, f)
				continue
			}
			if _, each := b.src.Attr("each"); each || b.src.AttrString("if") != "" {
				// The count of copies, or whether there is one, may change:
				// build the parent's children again.
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
	// A target inside another target is rebuilt with it.
	s.exp = newExpansion(s.opts.Limits)
	done := map[*box]bool{}
	for _, b := range targets {
		if done[b] || insideAny(b, done) {
			continue
		}
		s.rebuild(b)
		done[b] = true
	}
	return true
}

// formOf returns the nearest form box that holds b, or nil.
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

// rebuild evaluates a box again in place: its attributes, its widget and
// its children. The box keeps its place in the tree.
func (s *Surface) rebuild(b *box) {
	if b.kind == listItem {
		// An item's box is the list's to build: building its src (the list
		// node) here would nest a second list inside the item.
		s.rebuild(b.parent)
		return
	}
	if b == s.root {
		s.rebuildAll()
		s.full = true
		return
	}
	s.forget(b)
	nb := s.buildOne(b.src, b.ptr, b.scope, b.parent)
	if nb == nil {
		// if turned false: the parent builds its children again.
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

// forget drops b and its children from the pointer index.
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

// markView records that the view state of the node with key changed.
func (s *Surface) markView(key string) {
	s.viewDue = append(s.viewDue, key)
}

// vars returns the names an expression of a box can read.
func (s *Surface) vars(sc *scope) map[string]any {
	m := map[string]any{
		"state": s.store.State(),
		"view":  map[string]any{"width": float64(s.w), "height": float64(s.h), "band": band(s.w)},
	}
	sc.vars(m)
	return m
}

// band names the width class of a surface, for layouts that change with
// the width: narrow under 72 cells, medium under 120, wide from 120.
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

// template returns the parsed template of an attribute or text run.
func (s *Surface) template(n *tml.Node, key, src string) *tml.Template {
	c := s.compiledOf(n)
	t, ok := c.tmpl[key]
	if !ok {
		t, _ = tml.ParseTemplate(src)
		c.tmpl[key] = t
	}
	return t
}

// expr returns the parsed expression of an attribute such as if or each.
func (s *Surface) expr(n *tml.Node, key, src string) *tml.Expr {
	c := s.compiledOf(n)
	e, ok := c.exprs[key]
	if !ok {
		e, _ = tml.ParseBinding(src)
		c.exprs[key] = e
	}
	return e
}

// actionsOf returns the parsed actions of an action attribute.
func (s *Surface) actionsOf(n *tml.Node, attr string) []tml.Action {
	c := s.compiledOf(n)
	a, ok := c.actions[attr]
	if !ok {
		a, _ = tml.ParseActions(n.AttrString(attr))
		c.actions[attr] = a
	}
	return a
}

// value evaluates an attribute: a binding gives its value, a literal its
// own. The second result is false when the attribute is absent.
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

// str evaluates an attribute to text.
func (s *Surface) str(n *tml.Node, attr string, vars map[string]any) string {
	v, ok := s.value(n, attr, vars)
	if !ok || v == nil {
		return ""
	}
	return tml.Stringify(v)
}

// num evaluates an attribute to a number.
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

// count evaluates an attribute to a whole number in [lo, hi]. NaN and
// infinities give def, so no value from a document can size an allocation
// past hi, and the result is the same on every architecture.
func (s *Surface) count(n *tml.Node, attr string, vars map[string]any, def, lo, hi int) int {
	f := s.num(n, attr, vars, float64(def))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return def
	}
	return int(math.Max(float64(lo), math.Min(float64(hi), math.Trunc(f))))
}

// flag evaluates an attribute to a boolean.
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

// data evaluates a data attribute: a literal array, or a path such as
// state.items or item.kids. Anything else is nil.
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

// truthy evaluates if or show; an absent attribute is true.
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
