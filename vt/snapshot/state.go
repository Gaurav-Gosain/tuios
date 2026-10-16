package snapshot

import (
	"image/color"
	"reflect"
	"strconv"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"tuios.dev/vt"
)

// decscusrParam encodes a cursor shape as the DECSCUSR parameter a guest would
// have sent to ask for it, which is how it travels. Never zero: zero on the
// wire means the snapshot did not carry a shape.
func decscusrParam(style vt.CursorStyle, steady bool) int {
	n := int(style)*2 + 1
	if steady {
		n++
	}
	return n
}

// decscusrStyle is the inverse, for a parameter a snapshot actually carried.
func decscusrStyle(n int) (vt.CursorStyle, bool) {
	return vt.CursorStyle((n - 1) / 2), n%2 == 0
}

// State represents the serializable state of a terminal.
type State struct {
	// Seq is the stream position this snapshot was taken at: the emulator here
	// has consumed exactly the first Seq bytes the pane ever produced. A client
	// restoring this state subscribes from Seq, so it receives what came after
	// the snapshot and not what the snapshot already shows.
	Seq     int64 `json:"seq,omitempty"`
	Width   int   `json:"width"`    // screen width in cells
	Height  int   `json:"height"`   // screen height in cells
	CursorX int   `json:"cursor_x"` // cursor column, zero-based
	CursorY int   `json:"cursor_y"` // cursor row, zero-based
	// PendingWrap says the guest has just printed into the last column and
	// the cursor waits there with a wrap pending, so the next character it
	// prints starts the next row. Without it a snapshot taken at that moment
	// restored a cursor that printed over the last cell, and everything the
	// stream delivered after the snapshot landed one column out. Absent from
	// an older daemon, which reads as no wrap pending, as before.
	PendingWrap bool `json:"pending_wrap,omitempty"`
	// ScrollbackLen is the true length of the history, which can be more than
	// the Scrollback rows carried.
	ScrollbackLen int         `json:"scrollback_len"`
	IsAltScreen   bool        `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse event forwarding)
	Pen           *StyleState `json:"pen,omitempty"`           // Graphic rendition in force: what the guest's next output is painted with
	// Margins is the scroll region, as x, y, width, height. A guest sets it
	// once to hold a header or a status line out of the scrolling part of the
	// screen.
	Margins []int `json:"margins,omitempty"`
	// Charsets names the character set selected into each of G0 to G3 by its
	// designator byte, followed by the GL and GR slots. A program that draws
	// boxes selects the DEC line-drawing set once and then sends the box
	// characters as plain letters.
	Charsets []int `json:"charsets,omitempty"`
	// CursorShape is the shape the guest asked for, as the DECSCUSR parameter
	// that asks for it: 1 blinking block, 2 steady block, and so on up to 6 for
	// a steady bar. The guest's own spelling rather than a pair of fields
	// because zero then means "this snapshot does not say", which is what a
	// client restoring from an older daemon gets, and it leaves the pane on the
	// default instead of forcing a blinking block onto it.
	CursorShape   int          `json:"cursor_shape,omitempty"`
	Modes         map[int]bool `json:"modes,omitempty"`           // Terminal modes (mouse tracking, bracketed paste, etc.)
	KittyKbdStack []int        `json:"kitty_kbd_stack,omitempty"` // Kitty keyboard protocol flag stack, base entry first
	// KittyKbdMainStack is the main screen's kitty keyboard flag stack,
	// carried only while the alternate screen is active, when KittyKbdStack
	// is the alternate screen's.
	KittyKbdMainStack []int `json:"kitty_kbd_main_stack,omitempty"`
	// KittyKbdUnavailable says the pane does not offer the kitty keyboard
	// protocol, because its PTY is a Windows ConPTY. A client applies it to
	// its own emulator, so a guest that pushes flags without a query does not
	// switch the client to CSI u keys. False from an older daemon. See
	// vt.Emulator.DisableKittyKeyboardProtocol.
	KittyKbdUnavailable bool `json:"kitty_kbd_unavailable,omitempty"`
	// KittyImages is the ids the daemon gave the guest's kitty image numbers
	// (I=), and which ids hold an image. A client restoring this gives the
	// same ids from here on, so an image the guest places by the id it was
	// told is the same image on every client. Nil from an older daemon.
	KittyImages *vt.KittyGuestImagesState `json:"kitty_images,omitempty"`
	// ModifyOtherKeys is the xterm modifyOtherKeys level the guest set with
	// XTMODKEYS, 0 to 2. ModifyOtherKeysKnown says the daemon reported it:
	// a level of 0 is then the guest turning it off, which a client emulator
	// that kept an older level has to take, and not an older daemon that
	// says nothing.
	ModifyOtherKeys      int  `json:"modify_other_keys,omitempty"`
	ModifyOtherKeysKnown bool `json:"modify_other_keys_known,omitempty"` // see ModifyOtherKeys
	// Screen is the active screen, row by row.
	Screen [][]CellState `json:"screen"`
	// Scrollback is the newest history rows the snapshot carries, oldest first.
	Scrollback [][]CellState `json:"scrollback,omitempty"`
	// MainScreen is the normal screen, carried only while the alternate one is
	// active. It is the shell's screen underneath a full-screen program, which
	// quitting that program puts back on display. The alternate screen needs no
	// such treatment: entering it clears it, so what it held before is never
	// seen again.
	MainScreen [][]CellState `json:"main_screen,omitempty"`

	// The three grids above in packed form, sent instead of them when the
	// request asked (GetTerminalStatePayload.Packed). Styles is the table the
	// packed cells index, and is never empty when the cells are packed. See
	// snapshot_pack.go for the layout and why it exists.
	Styles           []StyleState `json:"styles,omitempty"`
	PackedScreen     []byte       `json:"packed_screen,omitempty"`     // Screen, packed
	PackedScrollback []byte       `json:"packed_scrollback,omitempty"` // Scrollback, packed
	PackedMain       []byte       `json:"packed_main,omitempty"`       // MainScreen, packed

	// ScreenWraps and ScrollbackWraps are the soft-wrap flags, one bit per
	// row (bit i of byte i/8, low bit first): the active screen's rows, and
	// the scrollback rows this snapshot carries, in the order it carries
	// them. A set bit says the row carries on to the next one because the
	// emulator wrapped it, where a row that is merely full ended with a
	// newline. A peer from before these fields sends neither, which reads as
	// no row wrapped: the safe answer, since a wrongly joined row glues two
	// lines into one link. See vt.Terminal.RestoreSoftWraps.
	ScreenWraps     []byte `json:"screen_wraps,omitempty"`
	ScrollbackWraps []byte `json:"scrollback_wraps,omitempty"` // soft-wrap flags of the Scrollback rows

	// ScreenPads and ScrollbackPads are, in the same layout, the rows that
	// wrapped a column early because a double-width character did not fit
	// in the last column, so that column is padding, not a typed space. A
	// reflow drops it when it joins the row to the next; without the flag
	// it keeps it, and the client and the daemon then lay the line out
	// apart. A peer from before these fields sends neither, which reads as
	// no padding: the line keeps a blank, and no text is lost.
	ScreenPads     []byte `json:"screen_pads,omitempty"`
	ScrollbackPads []byte `json:"scrollback_pads,omitempty"` // padding flags of the Scrollback rows

	// PenProtected says DECSCA protects what the guest prints next from a
	// selective erase (DECSED, DECSEL). Protected lists the cells of the
	// active screen it protected, as runs of three ints: row, column and
	// length. MainProtected is the same for the screen under an active
	// alternate one. A client without them erased protected cells the next
	// time the guest sent a selective erase. A peer from before these fields
	// sends none of them, which reads as nothing protected.
	PenProtected  bool  `json:"pen_protected,omitempty"`
	Protected     []int `json:"protected,omitempty"`      // protected runs of the active screen
	MainProtected []int `json:"main_protected,omitempty"` // protected runs of the main screen under the alternate one

	// LastPrinted is the character REP (CSI b) repeats, as the guest sent
	// it. LastPrintedKnown says the daemon reported it, so an empty one
	// means nothing has been printed since a reset, and not an older daemon
	// that says nothing.
	LastPrinted      string `json:"last_printed,omitempty"`
	LastPrintedKnown bool   `json:"last_printed_known,omitempty"` // see LastPrinted

	// SavedCursor is what DECSC saved on the active screen, which DECRC puts
	// back. MainSavedCursor is the main screen's, carried while the
	// alternate one is active: entering it with 1049 saved the shell's
	// cursor there, and leaving puts it back. A peer from before these
	// fields sends neither, and the client keeps the saved cursor it has.
	SavedCursor     *SavedCursorState `json:"saved_cursor,omitempty"`
	MainSavedCursor *SavedCursorState `json:"main_saved_cursor,omitempty"` // see SavedCursor

	// TabStops is the tab stop table, one bit per column in the layout of
	// ScreenWraps. It is never empty from a daemon that reports it, even
	// with every stop cleared, so a peer from before the field, which
	// sends none, reads as saying nothing, and the client keeps its own.
	TabStops []byte `json:"tab_stops,omitempty"`

	// Title, IconName and TitleStack are the window title, the icon name
	// and what XTWINOPS 22 saved, oldest first. A program saves the shell's
	// title when it starts and puts it back when it quits, so a client
	// restored without the stack keeps the program's title after it has
	// gone. TitlesKnown says the daemon reported them, so an empty title is
	// one the guest set and not an older daemon that says nothing.
	Title       string            `json:"title,omitempty"`
	IconName    string            `json:"icon_name,omitempty"`    // see Title
	TitleStack  []TitleEntryState `json:"title_stack,omitempty"`  // see Title
	TitlesKnown bool              `json:"titles_known,omitempty"` // see Title

	// GuestColors is what the guest set with OSC 4, 10, 11 and 12. A palette
	// entry decides how the guest's next SGR in that slot is painted, and
	// the three defaults answer its queries. Nil from an older daemon, and
	// the client keeps its own.
	GuestColors *GuestColorState `json:"guest_colors,omitempty"`

	// ANSIModes is insert mode (4) and newline mode (20), by number, which
	// Modes cannot carry: the two kinds of mode share its numbers. Nil from
	// an older daemon, and the client keeps its own.
	ANSIModes map[int]bool `json:"ansi_modes,omitempty"`

	// PendingInput is the input the daemon's parser is part way through
	// when the snapshot is taken: the start of an escape sequence or of a
	// UTF-8 character, at most vt.MaxPendingInput bytes. The rest of it
	// arrives on the stream after Seq, and a client at the ground state
	// would print that rest as text. See vt.Terminal.PendingInput.
	PendingInput []byte `json:"pending_input,omitempty"`

	// OpenCluster is the character cluster the daemon drew last and may
	// still extend: a snapshot taken between a base and its skin tone or
	// joiner has the rest arrive after Seq, and a client without the open
	// cluster draws the rest as a cluster of its own. Nil when none is open,
	// and from an emulator that does not report one.
	OpenCluster *OpenClusterState `json:"open_cluster,omitempty"`
}

// OpenClusterState is an open character cluster on the wire
// (vt.OpenCluster).
type OpenClusterState struct {
	Text  string `json:"text"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Width int    `json:"width"`
	Left  int    `json:"left"`
	Right int    `json:"right"`
}

// openClusters is an emulator that reports and restores its open cluster.
// The pure Go emulator is one; libghostty-vt keeps its own.
type openClusters interface {
	OpenCluster() (vt.OpenCluster, bool)
	RestoreOpenCluster(vt.OpenCluster)
}

// TitleEntryState is one entry of the title stack on the wire
// (vt.TitleEntry).
type TitleEntryState struct {
	Title    string `json:"title,omitempty"`     // saved window title
	Icon     string `json:"icon,omitempty"`      // saved icon name
	HasTitle bool   `json:"has_title,omitempty"` // whether the entry holds a title
	HasIcon  bool   `json:"has_icon,omitempty"`  // whether the entry holds an icon name
}

// GuestColorState is vt.GuestColors on the wire, each colour encoded by
// colorToWire. Palette lists only the slots the guest set.
type GuestColorState struct {
	Palette []PaletteColorState `json:"palette,omitempty"` // OSC 4 slots the guest set
	Fg      string              `json:"fg,omitempty"`      // OSC 10 default foreground
	Bg      string              `json:"bg,omitempty"`      // OSC 11 default background
	Cursor  string              `json:"cursor,omitempty"`  // OSC 12 cursor colour
}

// PaletteColorState is one palette slot the guest set.
type PaletteColorState struct {
	Index int    `json:"i"` // palette slot, 0 to 255
	Color string `json:"c"` // colour, encoded by colorToWire
}

func titlesToWire(state *State, ti vt.Titles) {
	state.Title, state.IconName, state.TitlesKnown = ti.Title, ti.Icon, true
	for _, e := range ti.Stack {
		state.TitleStack = append(state.TitleStack, TitleEntryState(e))
	}
}

func titlesFromWire(state *State) vt.Titles {
	ti := vt.Titles{Title: state.Title, Icon: state.IconName}
	for _, e := range state.TitleStack {
		ti.Stack = append(ti.Stack, vt.TitleEntry(e))
	}
	return ti
}

func guestColorsToWire(c vt.GuestColors) *GuestColorState {
	out := &GuestColorState{
		Fg:     colorToWire(c.Fg),
		Bg:     colorToWire(c.Bg),
		Cursor: colorToWire(c.Cursor),
	}
	for i, pc := range c.Palette {
		if pc != nil {
			out.Palette = append(out.Palette, PaletteColorState{Index: i, Color: colorToWire(pc)})
		}
	}
	return out
}

func guestColorsFromWire(t vt.Terminal, s *GuestColorState) vt.GuestColors {
	c := vt.GuestColors{
		Fg:     colorFromWire(t, s.Fg),
		Bg:     colorFromWire(t, s.Bg),
		Cursor: colorFromWire(t, s.Cursor),
	}
	for _, p := range s.Palette {
		if p.Index >= 0 && p.Index < len(c.Palette) {
			c.Palette[p.Index] = colorFromWire(t, p.Color)
		}
	}
	return c
}

// tabStopBits is the wire's bitset for a tab stop table width columns wide.
// It always has a byte, so a table with no stop still says so.
func tabStopBits(cols []int, width int) []byte {
	out := make([]byte, max((width+7)/8, 1))
	for _, x := range cols {
		if x >= 0 && x/8 < len(out) {
			out[x/8] |= 1 << (x % 8)
		}
	}
	return out
}

// tabStopCols reads the bitset back into the columns that hold a stop.
func tabStopCols(bits []byte) []int {
	cols := []int{}
	for i, b := range bits {
		for k := range 8 {
			if b&(1<<k) != 0 {
				cols = append(cols, i*8+k)
			}
		}
	}
	return cols
}

// SavedCursorState is a saved cursor on the wire (vt.SavedCursor).
type SavedCursorState struct {
	X           int         `json:"x,omitempty"`            // cursor column, zero-based
	Y           int         `json:"y,omitempty"`            // cursor row, zero-based
	Pen         *StyleState `json:"pen,omitempty"`          // rendition and hyperlink
	PendingWrap bool        `json:"pending_wrap,omitempty"` // wrap pending at the last column
	Origin      bool        `json:"origin,omitempty"`       // origin mode (DECOM)
	Protected   bool        `json:"protected,omitempty"`    // DECSCA protection
	// Charsets is the character set selection DECSC saved, in the layout of
	// State.Charsets.
	Charsets []int `json:"charsets,omitempty"`
}

func savedCursorToWire(c vt.SavedCursor) *SavedCursorState {
	pen := styleToWire(c.Pen, c.Link)
	return &SavedCursorState{
		X:           c.X,
		Y:           c.Y,
		Pen:         &pen,
		PendingWrap: c.PendingWrap,
		Origin:      c.Origin,
		Protected:   c.Protected,
		Charsets: []int{int(c.Charsets[0]), int(c.Charsets[1]), int(c.Charsets[2]),
			int(c.Charsets[3]), c.GL, c.GR},
	}
}

func savedCursorFromWire(t vt.Terminal, s SavedCursorState) vt.SavedCursor {
	c := vt.SavedCursor{
		X:           s.X,
		Y:           s.Y,
		PendingWrap: s.PendingWrap,
		Origin:      s.Origin,
		Protected:   s.Protected,
		Charsets:    [4]byte{'B', 'B', 'B', 'B'},
	}
	if s.Pen != nil {
		c.Pen, c.Link = styleFromWire(t, *s.Pen)
	}
	if len(s.Charsets) == 6 {
		for i := range 4 {
			c.Charsets[i] = byte(s.Charsets[i])
		}
		c.GL, c.GR = s.Charsets[4], s.Charsets[5]
	}
	return c
}

// runsToWire flattens protected runs into the wire's triples, or nil when
// there are none.
func runsToWire(runs []vt.CellRun) []int {
	if len(runs) == 0 {
		return nil
	}
	out := make([]int, 0, 3*len(runs))
	for _, r := range runs {
		out = append(out, r.Y, r.X, r.N)
	}
	return out
}

// runsFromWire reads the wire's triples back. A trailing partial triple is
// dropped; the emulator clips each run to its own grid.
func runsFromWire(flat []int) []vt.CellRun {
	runs := make([]vt.CellRun, 0, len(flat)/3)
	for i := 0; i+2 < len(flat); i += 3 {
		runs = append(runs, vt.CellRun{Y: flat[i], X: flat[i+1], N: flat[i+2]})
	}
	return runs
}

// wrapBits packs soft-wrap flags into the wire's bitset, or nil when none is
// set.
func wrapBits(flags []bool) []byte {
	var out []byte
	for i, f := range flags {
		if !f {
			continue
		}
		if out == nil {
			out = make([]byte, (len(flags)+7)/8)
		}
		out[i/8] |= 1 << (i % 8)
	}
	return out
}

// wrapFlags unpacks n flags from the wire's bitset. Bits past the end of the
// set read as false.
func wrapFlags(bits []byte, n int) []bool {
	out := make([]bool, max(n, 0))
	for i := range out {
		if i/8 < len(bits) {
			out[i] = bits[i/8]&(1<<(i%8)) != 0
		}
	}
	return out
}

// CellState represents a single terminal cell with full styling information.
//
// The attributes travel as the emulator's own bitmask rather than as a bool per
// attribute. Spelling them out one at a time is what left blink, conceal and
// strikethrough off the wire entirely, and collapsed the five underline styles
// into one.
type CellState struct {
	Content string `json:"c,omitempty"` // Cell content (character or grapheme)
	Width   int    `json:"w,omitempty"` // Cell width (1 for normal, 2 for wide chars, 0 for continuation)
	StyleState
}

// StyleState is a graphic rendition on the wire: how something is painted,
// separate from what it holds. It describes both a cell and the pen, which are
// the same rendition seen at two moments.
type StyleState struct {
	FgColor    string `json:"fg,omitempty"` // Foreground color, encoded by colorToWire
	BgColor    string `json:"bg,omitempty"` // Background color
	UlColor    string `json:"uc,omitempty"` // Underline color (SGR 58)
	Attrs      uint8  `json:"a,omitempty"`  // uv.Attr* bitmask: bold, faint, italic, blink, reverse, conceal, strikethrough
	Underline  uint8  `json:"u,omitempty"`  // ansi.Underline style: none, single, double, curly, dotted, dashed
	LinkURL    string `json:"l,omitempty"`  // OSC 8 hyperlink target
	LinkParams string `json:"lp,omitempty"` // OSC 8 hyperlink parameters
}

// styleToWire encodes a graphic rendition and the hyperlink that travels with it.
func styleToWire(s uv.Style, link uv.Link) StyleState {
	return StyleState{
		FgColor:    colorToWire(s.Fg),
		BgColor:    colorToWire(s.Bg),
		UlColor:    colorToWire(s.UnderlineColor),
		Attrs:      s.Attrs,
		Underline:  uint8(s.Underline),
		LinkURL:    link.URL,
		LinkParams: link.Params,
	}
}

// styleFromWire is styleToWire read back into the emulator that will hold it.
func styleFromWire(t vt.Terminal, ss StyleState) (uv.Style, uv.Link) {
	// The two values are named rather than returned as inline composite
	// literals, because gofmt 1.26 and 1.27 indent that construct differently
	// and each rejects the other's output. Naming them keeps the file
	// formatted the same way under both toolchains.
	style := uv.Style{
		Fg:             colorFromWire(t, ss.FgColor),
		Bg:             colorFromWire(t, ss.BgColor),
		UnderlineColor: colorFromWire(t, ss.UlColor),
		Underline:      ansi.Underline(ss.Underline),
		Attrs:          ss.Attrs,
	}
	link := uv.Link{
		URL:    ss.LinkURL,
		Params: ss.LinkParams,
	}
	return style, link
}

// colorToWire encodes a cell color so the client gets back the kind of color the
// guest asked for, not merely the shade it resolves to.
//
// A palette entry follows the user's terminal theme and the RGB it happens to
// resolve to does not. Flattening every color to hex meant a pane came back
// repainted in whichever shades the default palette gives: `31m` red became a
// fixed maroon, and a theme's own red was gone. That is the whole of the
// "colours randomly changing after a switch" report, and it was invisible to a
// comparison that read both sides through RGBA().
func colorToWire(c color.Color) string {
	switch v := c.(type) {
	case nil:
		return ""
	case ansi.BasicColor:
		if int(v) < len(basicColorWire) {
			return basicColorWire[v]
		}
		return "a" + strconv.Itoa(int(v))
	case ansi.IndexedColor:
		return indexedColorWire[v]
	}
	// A color.Color interface can hold a typed-nil pointer such as
	// (*color.RGBA)(nil). The case nil above matches only an untyped nil, and
	// RGBA has a value receiver, so reading it through a wrapped nil panics the
	// daemon. Encode it as the absence of color, the same as an untyped nil.
	if rv := reflect.ValueOf(c); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return ""
	}
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	buf := [7]byte{'#'}
	for i, v := range [3]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8)} {
		buf[1+2*i] = digits[v>>4]
		buf[2+2*i] = digits[v&0xf]
	}
	return string(buf[:])
}

// basicColorWire and indexedColorWire are every palette encoding colorToWire
// can produce, built once. A snapshot encodes a colour per cell, so building
// the string each time was one allocation per styled cell, per pane, per
// workspace switch.
var (
	basicColorWire   = paletteWire('a', 16)
	indexedColorWire = paletteWire('i', 256)
)

func paletteWire(prefix byte, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(prefix) + strconv.Itoa(i)
	}
	return out
}

// colorFromWire is colorToWire read back. Palette entries are resolved through
// the emulator that will hold them, so a restored cell is colored by the same
// rule as a cell the guest writes live into that emulator.
func colorFromWire(t vt.Terminal, s string) color.Color {
	if s == "" {
		return nil
	}
	if s[0] == '#' {
		// Read by hand rather than through fmt.Sscanf: this runs once per
		// coloured cell on the client's UI goroutine, and Sscanf was over
		// two microseconds of reflection per call, which put a truecolor
		// pane at twenty milliseconds per workspace switch.
		if len(s) != 7 {
			return nil
		}
		var rgb [3]byte
		for i := range rgb {
			hi, lo := hexNibble(s[1+2*i]), hexNibble(s[2+2*i])
			if hi < 0 || lo < 0 {
				return nil
			}
			rgb[i] = byte(hi<<4 | lo)
		}
		return color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil {
		return nil
	}
	switch s[0] {
	case 'a':
		return t.PaletteColor(n)
	case 'i':
		return t.IndexedColor(n)
	}
	return nil
}

// hexNibble is the value of one hex digit, or -1 for anything else.
func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// colorWireCache remembers the wire form of each RGB colour it has encoded.
// Palette colours have their strings built once for the process
// (basicColorWire, indexedColorWire); an RGB string is built per distinct
// colour per snapshot instead of per cell.
type colorWireCache map[color.RGBA]string

func (c colorWireCache) encode(col color.Color) string {
	rgba, ok := col.(color.RGBA)
	if !ok {
		return colorToWire(col)
	}
	if s, ok := c[rgba]; ok {
		return s
	}
	s := colorToWire(col)
	c[rgba] = s
	return s
}

// cellState is CellOf through the cache.
func (c colorWireCache) cellState(cell *uv.Cell) CellState {
	return CellState{
		Content: cell.Content,
		Width:   cell.Width,
		StyleState: StyleState{
			FgColor:    c.encode(cell.Style.Fg),
			BgColor:    c.encode(cell.Style.Bg),
			UlColor:    c.encode(cell.Style.UnderlineColor),
			Attrs:      cell.Style.Attrs,
			Underline:  uint8(cell.Style.Underline),
			LinkURL:    cell.Link.URL,
			LinkParams: cell.Link.Params,
		},
	}
}

// CellOf converts a VT cell to a serializable CellState.
func CellOf(cell *uv.Cell) CellState {
	if cell == nil {
		return CellState{}
	}

	return CellState{
		Content:    cell.Content,
		Width:      cell.Width,
		StyleState: styleToWire(cell.Style, cell.Link),
	}
}
