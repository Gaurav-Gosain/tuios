// Package vtgen generates terminal input that looks like something a program
// would actually emit.
//
// Random bytes are a poor fuzzer for a VT emulator. The parser rejects almost
// all of them in its first state, so a campaign spends its budget proving that
// garbage is garbage and never reaches the code that moves the cursor, sizes a
// scroll region or decides how many cells a character takes. What breaks an
// emulator is a well-formed sequence carrying a parameter nobody expected: a
// scroll region sized for a taller screen, a repeat count of two billion, a
// colour with four components, a cluster split across two writes.
//
// So this generates by grammar rather than by byte. Every draw is a real
// sequence with a name attached, which buys two things: the interesting states
// are reachable at all, and a failing run reduces to a script a person can read
// and retype rather than to a hexdump.
package vtgen

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// Seq is one step of a script: bytes to write, or a resize, with a description
// of what it is meant to be.
type Seq struct {
	// Kind groups steps for the shrinker and for reading. One of "text",
	// "cc", "esc", "csi", "sgr", "mode", "osc", "dcs", "apc", "margin",
	// "erase", "tabs", "resize".
	Kind string

	// Bytes is what gets written to the emulator. Empty for a resize.
	Bytes string

	// Desc names the sequence the way a person would say it.
	Desc string

	// Cols and Rows are the new size when Kind is "resize".
	Cols, Rows int
}

// Script is a run.
type Script []Seq

// Bytes concatenates everything a script writes, ignoring its resizes. Callers
// that care about ordering with respect to resizes should walk the steps.
func (s Script) Bytes() string {
	var b strings.Builder
	for _, seq := range s {
		b.WriteString(seq.Bytes)
	}
	return b.String()
}

// SplitWrites chops everything a script writes into the pieces a reader would
// actually hand the parser, at boundaries drawn from src. A PTY read boundary
// falls wherever the kernel put it: inside a multi-byte character, halfway
// through a CSI parameter list, between a base character and the mark that
// belongs to it. A parser that only ever sees whole sequences is not the parser
// that runs in production, and the state it has to carry across a boundary is
// the state nothing else exercises.
//
// The pieces are small most of the time so those splits happen constantly
// rather than by luck, with an occasional larger read so a run is not uniformly
// one byte at a time. Resizes are dropped, the way Bytes drops them.
func (s Script) SplitWrites(src uint64) []string {
	b := s.Bytes()
	if b == "" {
		return nil
	}
	r := rand.New(rand.NewPCG(src, src^0x9e3779b97f4a7c15))
	var out []string
	for i := 0; i < len(b); {
		var n int
		switch r.Uint64() % 16 {
		case 0:
			n = 1 + int(r.Uint64()%64)
		case 1, 2, 3, 4, 5:
			n = 1
		default:
			n = 1 + int(r.Uint64()%4)
		}
		n = min(n, len(b)-i)
		out = append(out, b[i:i+n])
		i += n
	}
	return out
}

// String renders a script as something a person can read and retype. This is
// the whole point of generating by grammar: a failing run prints as a dozen
// named lines rather than as a wall of escape bytes.
func (s Script) String() string {
	var b strings.Builder
	for i, seq := range s {
		fmt.Fprintf(&b, "%3d  %-6s %-28s %s\n", i+1, seq.Kind, quote(seq.Bytes), seq.Desc)
	}
	return b.String()
}

// quote renders bytes with escapes a reader can paste into a shell.
func quote(s string) string {
	if s == "" {
		return "-"
	}
	q := strconv.Quote(s)
	return strings.ReplaceAll(q[1:len(q)-1], `\x1b`, `\e`)
}

// source is where the randomness comes from. Two implementations feed the same
// generator: a seeded PRNG for a local sweep, and a byte reader so that a
// `go test -fuzz` corpus entry always decodes to the same script and a
// coverage-guided mutation of the bytes is a mutation of the script.
type source interface{ next() uint64 }

type prngSource struct{ r *rand.Rand }

func (p prngSource) next() uint64 { return p.r.Uint64() }

// byteSource reads eight bytes at a time and falls through to a PRNG seeded
// from the input once the input is spent, so a short corpus entry still decodes
// to a long run while the mutator keeps control of the prefix.
type byteSource struct {
	b    []byte
	i    int
	tail *rand.Rand
}

func newByteSource(b []byte) *byteSource {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return &byteSource{b: b, tail: rand.New(rand.NewPCG(h, h^0x9e3779b97f4a7c15))}
}

func (s *byteSource) next() uint64 {
	if s.i+8 > len(s.b) {
		return s.tail.Uint64()
	}
	var v uint64
	for _, c := range s.b[s.i : s.i+8] {
		v = v<<8 | uint64(c)
	}
	s.i += 8
	return v
}

// Gen draws sequences.
type Gen struct {
	src source

	// cols and rows track the size the generator last asked for, so that the
	// parameters it picks are sometimes exactly wrong for the current screen
	// rather than uniformly random. A region sized for the previous screen is
	// the shape that has actually crashed terminals.
	cols, rows int
}

// New seeds a generator for a local sweep. The same seed always yields the same
// script.
func New(seed uint64) *Gen {
	return &Gen{src: prngSource{rand.New(rand.NewPCG(seed, seed^0xda3e39cb94b95bdb))}, cols: 80, rows: 24}
}

// FromBytes decodes a `go test -fuzz` input into a script.
func FromBytes(b []byte) *Gen {
	return &Gen{src: newByteSource(b), cols: 80, rows: 24}
}

func (g *Gen) u(n int) int {
	if n <= 0 {
		return 0
	}
	return int(g.src.next() % uint64(n))
}

func (g *Gen) pick(ss []string) string { return ss[g.u(len(ss))] }

// Script draws n steps.
func (g *Gen) Script(n int) Script {
	out := make(Script, 0, n)
	for range n {
		out = append(out, g.Next())
	}
	return out
}

// kinds are drawn with these weights. Text and CSI carry most of it because
// that is what a program emits most of; DCS and APC are rarer but each one
// exercises a whole separate parser state, so they are not left to chance.
var kindWeights = []struct {
	kind string
	w    int
}{
	{"text", 130},
	{"csi", 150},
	{"sgr", 60},
	{"mode", 55},
	{"cc", 45},
	{"esc", 35},
	{"osc", 40},
	{"dcs", 22},
	{"apc", 12},
	{"margin", 35},
	{"erase", 30},
	{"tabs", 20},
	{"resize", 18},
}

var kindWeightSum = func() int {
	n := 0
	for _, k := range kindWeights {
		n += k.w
	}
	return n
}()

// Next draws one step.
func (g *Gen) Next() Seq {
	seq := g.draw()
	// A guest on an eight-bit channel introduces a sequence with one C1 byte
	// rather than with ESC and a second byte. It means the same thing and it
	// enters the parser through a different door, so an emulator can handle
	// every seven-bit form and still drop every eight-bit one.
	if g.u(20) == 0 {
		if rewritten, ok := eightBit(seq); ok {
			return rewritten
		}
	}
	return seq
}

func (g *Gen) draw() Seq {
	n := g.u(kindWeightSum)
	for _, k := range kindWeights {
		if n < k.w {
			return g.of(k.kind)
		}
		n -= k.w
	}
	return g.text()
}

// eightBit rewrites a seven-bit introducer to the single C1 byte that means the
// same thing, and the ST that closes a string with it. It reports whether it
// found anything to rewrite, so a step with no introducer is left alone rather
// than relabelled as something it is not.
func eightBit(seq Seq) (Seq, bool) {
	var b string
	switch {
	case strings.HasPrefix(seq.Bytes, "\x1b["):
		b = "\x9b" + seq.Bytes[2:]
	case strings.HasPrefix(seq.Bytes, "\x1b]"):
		b = "\x9d" + seq.Bytes[2:]
	case strings.HasPrefix(seq.Bytes, "\x1bP"):
		b = "\x90" + seq.Bytes[2:]
	case strings.HasPrefix(seq.Bytes, "\x1b_"):
		b = "\x9f" + seq.Bytes[2:]
	default:
		return seq, false
	}
	if strings.HasSuffix(b, "\x1b\\") {
		b = b[:len(b)-2] + "\x9c"
	}
	seq.Bytes = b
	seq.Desc += ", using eight-bit controls"
	return seq, true
}

func (g *Gen) of(kind string) Seq {
	switch kind {
	case "text":
		return g.text()
	case "csi":
		return g.csi()
	case "sgr":
		return g.sgr()
	case "mode":
		return g.mode()
	case "cc":
		return g.cc()
	case "esc":
		return g.esc()
	case "osc":
		return g.osc()
	case "dcs":
		return g.dcs()
	case "apc":
		return g.apc()
	case "margin":
		return g.margin()
	case "erase":
		return g.erase()
	case "tabs":
		return g.tabs()
	case "resize":
		return g.resize()
	}
	return g.text()
}

// param draws a parameter value from a distribution weighted to the values that
// have broken terminals rather than uniformly over the integers. A uniform draw
// is almost always a number no code path treats specially.
//
// The empty return is a parameter the guest omitted, which is its own case:
// omitted takes the default while an explicit zero often does not.
func (g *Gen) param() string {
	switch g.u(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11:
		return "" // omitted
	case 12, 13, 14, 15, 16, 17, 18, 19:
		return "0"
	case 20, 21, 22, 23:
		return "1"
	case 24, 25, 26:
		// Exactly the current screen, and exactly one past it. A region or an
		// address that fits the screen the guest thinks it has is the shape
		// that survives a resize and then indexes off the end.
		return strconv.Itoa(g.rows)
	case 27, 28:
		return strconv.Itoa(g.rows + 1)
	case 29, 30:
		return strconv.Itoa(g.cols)
	case 31, 32:
		return strconv.Itoa(g.cols + 1)
	case 33, 34, 35:
		return "65535"
	case 36, 37, 38:
		return "65536"
	case 39, 40:
		return "2147483647"
	case 41, 42:
		return "4294967295"
	case 43, 44:
		return "99999999999999999999"
	default:
		return strconv.Itoa(g.u(120))
	}
}

// params builds a parameter list. It sometimes uses a colon rather than a
// semicolon, which is the separator SGR subparameters use and which every other
// sequence has to survive seeing.
func (g *Gen) params(maxN int) string {
	n := g.u(maxN + 1)
	if n == 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = g.param()
	}
	sep := ";"
	if g.u(12) == 0 {
		sep = ":"
	}
	s := strings.Join(parts, sep)
	if g.u(24) == 0 {
		// A trailing separator leaves an empty final parameter, which is a
		// different thing from having one fewer.
		s += sep
	}
	return s
}

// csiFinals are the final bytes worth spending draws on, paired with the name a
// person would use. Reports are included: they write a reply into a bounded
// pipe, and a run that fills that pipe is its own failure mode.
var csiFinals = []struct {
	final byte
	name  string
}{
	{'@', "ICH insert characters"},
	{'A', "CUU cursor up"},
	{'B', "CUD cursor down"},
	{'C', "CUF cursor forward"},
	{'D', "CUB cursor back"},
	{'E', "CNL next line"},
	{'F', "CPL previous line"},
	{'G', "CHA column address"},
	{'H', "CUP cursor position"},
	{'I', "CHT forward tab"},
	{'J', "ED erase in display"},
	{'K', "EL erase in line"},
	{'L', "IL insert lines"},
	{'M', "DL delete lines"},
	{'P', "DCH delete characters"},
	{'S', "SU scroll up"},
	{'T', "SD scroll down"},
	{'X', "ECH erase characters"},
	{'Z', "CBT backward tab"},
	{'`', "HPA column position"},
	{'a', "HPR column forward"},
	{'b', "REP repeat last character"},
	{'c', "DA device attributes"},
	{'d', "VPA row position"},
	{'e', "VPR row forward"},
	{'f', "HVP cursor position"},
	{'g', "TBC clear tab stop"},
	{'n', "DSR device status"},
	{'r', "DECSTBM top and bottom margins"},
	{'s', "DECSLRM left and right margins, or save cursor"},
	{'t', "XTWINOPS window operation"},
}

func (g *Gen) csi() Seq {
	f := csiFinals[g.u(len(csiFinals))]
	p := g.params(4)

	var prefix, inter string
	switch g.u(24) {
	case 0:
		prefix = "?"
	case 1:
		prefix = ">"
	case 2:
		prefix = "<"
	case 3:
		prefix = "="
	case 4:
		inter = " "
	case 5:
		inter = "$"
	case 6:
		inter = "!"
	}

	bytes := "\x1b[" + prefix + p + inter + string(f.final)

	// One in a while the sequence is cut off part-way, which leaves the parser
	// in the middle of a state with the next sequence arriving behind it.
	if g.u(30) == 0 {
		bytes = "\x1b[" + prefix + p
		return Seq{Kind: "csi", Bytes: bytes, Desc: "truncated CSI, no final byte"}
	}
	// And once in a while an abort lands inside it.
	if g.u(40) == 0 {
		bytes = "\x1b[" + prefix + p + "\x18" + string(f.final)
		return Seq{Kind: "csi", Bytes: bytes, Desc: "CSI aborted by CAN, then " + string(f.final)}
	}

	return Seq{Kind: "csi", Bytes: bytes, Desc: fmt.Sprintf("%s%s", f.name, describeParams(prefix, p, inter))}
}

func describeParams(prefix, p, inter string) string {
	var parts []string
	if prefix != "" {
		parts = append(parts, "private "+prefix)
	}
	if p != "" {
		parts = append(parts, "params "+p)
	}
	if inter != "" {
		parts = append(parts, "intermediate "+strconv.Quote(inter))
	}
	if len(parts) == 0 {
		return " (no parameters)"
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// sgrPieces are the attribute codes, spelled out so a script says what it did.
var sgrPieces = []struct {
	body string
	name string
}{
	{"0", "reset"},
	{"1", "bold"},
	{"2", "faint"},
	{"3", "italic"},
	{"4", "underline"},
	{"4:0", "underline off by subparameter"},
	{"4:2", "double underline"},
	{"4:3", "curly underline"},
	{"4:5", "dashed underline"},
	{"4:7", "underline style out of range"},
	{"5", "blink"},
	{"7", "reverse"},
	{"8", "conceal"},
	{"9", "strikethrough"},
	{"21", "double underline, or not bold"},
	{"22", "normal intensity"},
	{"23", "not italic"},
	{"24", "not underlined"},
	{"27", "not reversed"},
	{"29", "not struck"},
	{"39", "default foreground"},
	{"49", "default background"},
	{"59", "default underline colour"},
	{"38;5;196", "256-colour foreground"},
	{"48;5;16", "256-colour background"},
	{"58;5;9", "256-colour underline"},
	{"38;2;255;128;0", "truecolour foreground"},
	{"48;2;0;0;0", "truecolour background"},
	{"58;2;1;2;3", "truecolour underline"},
	{"38:2::255:128:0", "truecolour foreground, colon form"},
	{"58:2::1:2:3", "truecolour underline, colon form"},
	{"38;2;999;999;999", "truecolour components out of range"},
	{"38;5", "256-colour with the index missing"},
	{"38;2;1", "truecolour with two components missing"},
	{"38", "colour introducer with nothing after it"},
	{"", "empty, which means reset"},
	{"90", "bright foreground"},
	{"100", "bright background"},
	{"123", "an attribute code nobody defines"},
}

func (g *Gen) sgr() Seq {
	n := 1 + g.u(3)
	parts := make([]string, n)
	names := make([]string, n)
	for i := range parts {
		p := sgrPieces[g.u(len(sgrPieces))]
		parts[i] = p.body
		names[i] = p.name
	}
	return Seq{
		Kind:  "sgr",
		Bytes: "\x1b[" + strings.Join(parts, ";") + "m",
		Desc:  "SGR " + strings.Join(names, " then "),
	}
}

// modes worth toggling, with the number a person would recognise them by.
var modes = []struct {
	n    string
	name string
}{
	{"1", "DECCKM application cursor keys"},
	{"3", "DECCOLM column mode"},
	{"5", "DECSCNM reverse video"},
	{"6", "DECOM origin mode"},
	{"7", "DECAWM autowrap"},
	{"12", "cursor blink"},
	{"25", "DECTCEM cursor visible"},
	{"47", "alternate screen, the original one"},
	{"66", "DECNKM numeric keypad"},
	{"69", "DECLRMM left and right margin mode"},
	{"1000", "mouse reporting"},
	{"1002", "mouse button tracking"},
	{"1003", "mouse any-event tracking"},
	{"1006", "SGR mouse encoding"},
	{"1047", "alternate screen, no cursor save"},
	{"1048", "save and restore the cursor"},
	{"1049", "alternate screen with cursor save"},
	{"2004", "bracketed paste"},
	{"1004", "focus reporting"},
	{"2026", "synchronised output"},
	// Grapheme clustering changes how wide a cluster is, so an emulator that
	// honours it and one that does not lay the same text out differently. The
	// ghostty differential harness turned that into its loudest finding.
	{"2027", "grapheme clustering"},
	{"2048", "in-band resize reports"},
	{"9999", "a private mode nobody defines"},
}

func (g *Gen) mode() Seq {
	m := modes[g.u(len(modes))]
	set := g.u(2) == 0
	final := "l"
	verb := "reset"
	if set {
		final = "h"
		verb = "set"
	}
	private := "?"
	if g.u(16) == 0 {
		// The same numbers without the private marker are ANSI modes, a
		// different table entirely, and a guest does emit them by mistake.
		private = ""
	}
	return Seq{
		Kind:  "mode",
		Bytes: "\x1b[" + private + m.n + final,
		Desc:  verb + " " + m.name,
	}
}

var controls = []struct {
	b    byte
	name string
}{
	{0x07, "BEL"},
	{0x08, "BS backspace"},
	{0x09, "HT tab"},
	{0x0a, "LF linefeed"},
	{0x0b, "VT"},
	{0x0c, "FF"},
	{0x0d, "CR"},
	{0x0e, "SO shift out"},
	{0x0f, "SI shift in"},
	{0x00, "NUL"},
	{0x7f, "DEL"},
}

func (g *Gen) cc() Seq {
	c := controls[g.u(len(controls))]
	n := 1 + g.u(4)
	return Seq{
		Kind:  "cc",
		Bytes: strings.Repeat(string(c.b), n),
		Desc:  fmt.Sprintf("%s x%d", c.name, n),
	}
}

var escapes = []struct {
	body string
	name string
}{
	{"7", "DECSC save cursor"},
	{"8", "DECRC restore cursor"},
	{"D", "IND index"},
	{"E", "NEL next line"},
	{"H", "HTS set tab stop"},
	{"M", "RI reverse index"},
	{"c", "RIS full reset"},
	{"=", "DECKPAM keypad application"},
	{">", "DECKPNM keypad numeric"},
	{"#8", "DECALN alignment pattern"},
	{"(0", "designate the line-drawing set as G0"},
	{"(B", "designate ASCII as G0"},
	{"(A", "designate the UK set as G0"},
	{")0", "designate the line-drawing set as G1"},
	{"n", "LS2 lock shift G2"},
	{"o", "LS3 lock shift G3"},
	{"~", "LS1R lock shift G1 right"},
	{"N", "SS2 single shift G2"},
	{"O", "SS3 single shift G3"},
	{"*B", "designate ASCII as G2"},
	{"+B", "designate ASCII as G3"},
	{"(1", "an SCS final nothing defines"},
	{"%G", "select UTF-8"},
	{"%@", "select the default encoding"},
	{"#3", "double-height top half"},
	{"#4", "double-height bottom half"},
	{"#5", "single-width line"},
	{"#6", "double-width line"},
	{"}", "LS2R lock shift G2 right"},
	{"|", "LS3R lock shift G3 right"},
}

func (g *Gen) esc() Seq {
	e := escapes[g.u(len(escapes))]
	return Seq{Kind: "esc", Bytes: "\x1b" + e.body, Desc: e.name}
}

func (g *Gen) osc() Seq {
	// The terminator is its own axis: BEL and ST both end an OSC, and a string
	// that never ends is what a truncated write looks like.
	term, termName := "\x07", "BEL"
	switch g.u(10) {
	case 0, 1, 2, 3:
		term, termName = "\x1b\\", "ST"
	case 4:
		term, termName = "", "no terminator"
	}

	body, name := g.oscBody()
	return Seq{
		Kind:  "osc",
		Bytes: "\x1b]" + body + term,
		Desc:  name + ", terminated by " + termName,
	}
}

func (g *Gen) oscBody() (string, string) {
	switch g.u(16) {
	case 0:
		return "0;" + g.pick(titles), "OSC 0 set icon name and title"
	case 1:
		return "2;" + g.pick(titles), "OSC 2 set title"
	case 2:
		return "7;file://host/tmp/" + g.pick(titles), "OSC 7 report working directory"
	case 3:
		return "8;;https://example.invalid/" + g.pick(titles), "OSC 8 hyperlink"
	case 4:
		return "8;id=" + g.pick(titles) + ";https://example.invalid/", "OSC 8 hyperlink with an id"
	case 5:
		return "8;;", "OSC 8 hyperlink closed"
	case 6:
		return "9;" + g.pick(titles), "OSC 9 notification"
	case 7:
		return "9;4;" + g.param() + ";" + g.param(), "OSC 9;4 progress"
	case 8:
		return "52;c;" + g.pick(base64ish), "OSC 52 clipboard write"
	case 9:
		return "52;c;?", "OSC 52 clipboard read"
	case 10:
		return "777;notify;" + g.pick(titles) + ";" + g.pick(titles), "OSC 777 notification"
	case 11:
		return "10;?", "OSC 10 query foreground"
	case 12:
		return "11;rgb:00/00/00", "OSC 11 set background"
	case 13:
		return "4;" + g.param() + ";?", "OSC 4 query a palette entry"
	case 14:
		return strconv.Itoa(g.u(2000)) + ";" + g.pick(titles), "an OSC number nobody defines"
	default:
		return "0;" + strings.Repeat("A", 1<<uint(10+g.u(6))), "OSC 0 with an oversized payload"
	}
}

var titles = []string{
	"t",
	"a title",
	"世界",
	"é",
	"\U0001f469‍\U0001f4bb",
	"",
	";",
	"\x07",
	"a\x1bb",
	strings.Repeat("x", 300),
}

var base64ish = []string{
	"aGVsbG8=",
	"",
	"!!!not base64!!!",
	strings.Repeat("QUJD", 400),
}

func (g *Gen) dcs() Seq {
	// The two multiplexer passthrough wrappings are the ones with a rule that
	// is easy to get backwards: screen takes the inner sequence as-is, tmux
	// requires every inner ESC to be doubled. A terminal that treats them the
	// same corrupts one of them.
	inner := g.pick(passthroughInner)
	switch g.u(8) {
	case 0:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bPtmux;" + strings.ReplaceAll(inner, "\x1b", "\x1b\x1b") + "\x1b\\",
			Desc:  "tmux passthrough, inner ESC doubled as tmux requires",
		}
	case 1:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bPtmux;" + inner + "\x1b\\",
			Desc:  "tmux passthrough with the inner ESC left single, which is malformed",
		}
	case 2:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP" + inner + "\x1b\\",
			Desc:  "screen passthrough, inner sequence as-is",
		}
	case 3:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP" + strings.ReplaceAll(inner, "\x1b", "\x1b\x1b") + "\x1b\\",
			Desc:  "screen passthrough with the inner ESC doubled, which is malformed",
		}
	case 4:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP$q" + g.pick([]string{"m", "r", "s", " q", `"p`}) + "\x1b\\",
			Desc:  "DECRQSS request a setting",
		}
	case 5:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP" + g.params(3) + "q" + strings.Repeat("#0~", 1+g.u(40)) + "\x1b\\",
			Desc:  "sixel data",
		}
	case 6:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP" + g.params(3) + "|" + strings.Repeat("A", 1<<uint(8+g.u(8))),
			Desc:  "DCS with an oversized payload and no terminator",
		}
	default:
		return Seq{
			Kind:  "dcs",
			Bytes: "\x1bP+q" + g.pick([]string{"544e", "6b656e6421", ""}) + "\x1b\\",
			Desc:  "XTGETTCAP terminfo query",
		}
	}
}

var passthroughInner = []string{
	"\x1b]0;inner\x07",
	"\x1b[31m",
	"\x1b[2J",
	"\x1b_Gf=24,s=1,v=1;AAAA\x1b\\",
	"\x1b[?1049h",
}

func (g *Gen) apc() Seq {
	switch g.u(4) {
	case 0:
		return Seq{
			Kind:  "apc",
			Bytes: "\x1b_Gf=24,s=" + strconv.Itoa(1+g.u(64)) + ",v=" + strconv.Itoa(1+g.u(64)) + ";" + strings.Repeat("QUJD", 1+g.u(64)) + "\x1b\\",
			Desc:  "kitty graphics, transmit an image",
		}
	case 1:
		return Seq{
			Kind:  "apc",
			Bytes: "\x1b_Ga=d\x1b\\",
			Desc:  "kitty graphics, delete placements",
		}
	case 2:
		return Seq{
			Kind:  "apc",
			Bytes: "\x1b_G" + g.pick([]string{"a=q", "a=T,f=100", "i=1,a=p"}) + ";" + strings.Repeat("A", 1<<uint(6+g.u(10))) + "\x1b\\",
			Desc:  "kitty graphics with an oversized payload",
		}
	default:
		return Seq{
			Kind:  "apc",
			Bytes: "\x1b_" + strings.Repeat("Z", 1+g.u(200)),
			Desc:  "an APC string that never ends",
		}
	}
}

// shown names a single parameter for a description, keeping an omitted one
// visible. An omitted parameter takes the default where an explicit zero often
// does not, so a report that renders both as nothing hides the difference that
// mattered.
func shown(p string) string {
	if p == "" {
		return "with the parameter omitted"
	}
	return "with parameter " + p
}

// pair names a two-parameter value the way a reader would say it back.
func pair(a, b string) string {
	name := func(s string) string {
		if s == "" {
			return "omitted"
		}
		return s
	}
	return name(a) + " and " + name(b)
}

// compose builds one step out of several sequences that only mean anything
// together. Drawing them one at a time would reach the combination roughly
// never, and the shrinker still works because it reduces whole steps.
type compose struct {
	bytes []string
	names []string
}

func (c *compose) add(bytes, name string) {
	c.bytes = append(c.bytes, bytes)
	c.names = append(c.names, name)
}

func (c *compose) seq(kind string) Seq {
	return Seq{Kind: kind, Bytes: strings.Join(c.bytes, ""), Desc: strings.Join(c.names, ", then ")}
}

// margin sets up left and right margins as a whole rather than one sequence at
// a time. The failure being hunted needs several things true at once: DECLRMM
// enabled, both margin pairs holding values sized for a screen that is about to
// be resized, and a cursor addressed under origin mode so its coordinates are
// read against margins that may no longer fit.
func (g *Gen) margin() Seq {
	var c compose

	if g.u(3) == 0 {
		c.add("\x1b[?6h", "set DECOM origin mode")
	}
	if g.u(4) == 0 {
		c.add("\x1b7", "DECSC save cursor")
	}
	c.add("\x1b[?69h", "enable DECLRMM")

	left, right := g.param(), g.param()
	c.add("\x1b["+left+";"+right+"s", "DECSLRM left and right margins "+pair(left, right))

	top, bottom := g.param(), g.param()
	c.add("\x1b["+top+";"+bottom+"r", "DECSTBM top and bottom margins "+pair(top, bottom))

	if g.u(2) == 0 {
		row, col := g.param(), g.param()
		c.add("\x1b["+row+";"+col+"H", "CUP cursor position "+pair(row, col))
	}
	if g.u(4) == 0 {
		c.add("\x1b8", "DECRC restore cursor")
	}
	if g.u(5) == 0 {
		// Turning the mode off while the margins it gated are still set leaves
		// state a terminal has to decide about, and forgetting to clear it is
		// how a margin outlives the mode that allowed it.
		c.add("\x1b[?69l", "reset DECLRMM with the margins still set")
	}
	return c.seq("margin")
}

// eraseParams are the erase selectors worth drawing: the three values ED and EL
// define, the scrollback one only some terminals have, and values past the end
// of the table.
var eraseParams = []string{"", "0", "1", "2", "3", "4", "9"}

// decscaParams are the protection selectors, again with values nothing defines.
var decscaParams = []string{"", "0", "1", "2", "3", "255"}

// erase draws the erase family together with the protection attribute that
// changes what erasing means and the resets meant to clear it. DECSCA on its
// own does nothing visible. The case that breaks is a selective erase running
// over cells marked protected, and then a reset that has to drop both the
// protection on those cells and the attribute still being applied to new ones.
func (g *Gen) erase() Seq {
	var c compose

	if g.u(2) == 0 {
		p := g.pick(decscaParams)
		c.add("\x1b["+p+"\"q", "DECSCA character protection "+shown(p))
	}

	p := g.pick(eraseParams)
	switch g.u(4) {
	case 0:
		c.add("\x1b[?"+p+"J", "DECSED selective erase in display "+shown(p))
	case 1:
		c.add("\x1b[?"+p+"K", "DECSEL selective erase in line "+shown(p))
	case 2:
		c.add("\x1b["+p+"J", "ED erase in display "+shown(p))
	default:
		c.add("\x1b["+p+"K", "EL erase in line "+shown(p))
	}

	switch g.u(6) {
	case 0:
		c.add("\x1b[!p", "DECSTR soft reset")
	case 1:
		c.add("\x1bc", "RIS full reset")
	}
	return c.seq("erase")
}

// tbcParams are the tab-clear selectors, including two nothing defines.
var tbcParams = []string{"", "0", "2", "3", "5", "9"}

// tabs walks the tab-stop table, which nothing else here touches. The table is
// state a resize has to carry, and a stop left past the new width after a
// shrink is what the next tab indexes with.
func (g *Gen) tabs() Seq {
	var c compose

	switch g.u(8) {
	case 0:
		c.add("\x1bH", "HTS set a tab stop at the cursor")
	case 1:
		n := 1 + g.u(12)
		c.add(strings.Repeat("\t", n), fmt.Sprintf("HT tab x%d", n))
	case 2:
		p := g.pick(tbcParams)
		c.add("\x1b["+p+"g", "TBC clear tab stops "+shown(p))
	case 3:
		p := g.param()
		c.add("\x1b["+p+"I", "CHT forward tab "+shown(p))
	case 4:
		p := g.param()
		c.add("\x1b["+p+"Z", "CBT backward tab "+shown(p))
	case 5:
		c.add("\x1b[?5W", "DECST8C reset tab stops to every eight columns")
	case 6:
		p := g.param()
		c.add("\x1b["+p+"W", "tab-stop set "+shown(p))
	default:
		// Setting a stop and then jumping to it is the pair that says whether
		// the table survived what came between them.
		p := g.param()
		c.add("\x1b["+p+"G", "CHA column address "+shown(p))
		c.add("\x1bH", "HTS set a tab stop there")
		c.add("\t", "HT tab")
	}
	return c.seq("tabs")
}

// texts are drawn from the classes that decide layout: a width, a cluster
// boundary, or an encoding the decoder has to reject.
var texts = []struct {
	s    string
	name string
}{
	{"hello", "plain ASCII"},
	{"$ ", "a shell prompt"},
	{"a\tb\tc", "ASCII with tabs"},
	{strings.Repeat("x", 250), "a line long enough to wrap several times"},
	{"世界", "wide characters, two cells each"},
	{"ＡＢ", "fullwidth Latin"},
	{"ｱｲ", "halfwidth katakana"},
	{"한글", "hangul"},
	{"¡±†", "ambiguous width"},
	{"é", "an ASCII base with a combining mark"},
	{"é̂̃", "an ASCII base under three marks"},
	{"世́", "a wide base with a combining mark"},
	{"\U0001f44d", "an emoji"},
	{"\U0001f44d\U0001f3fd", "an emoji with a skin tone"},
	{"\U0001f469‍\U0001f469‍\U0001f467", "a zero-width-joiner family"},
	{"\U0001f1fa\U0001f1f8", "a regional indicator pair"},
	{"\U0001f1fa\U0001f1f8\U0001f1fa", "three regional indicators"},
	{"❤️", "a base with an emoji presentation selector"},
	{"❤︎", "a base with a text presentation selector"},
	{"1️⃣", "a keycap sequence"},
	{"\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", "a tag sequence flag"},
	{"​", "a zero-width space"},
	{"؀b", "a prepend character before an ASCII base"},
	{"العربية", "right-to-left text"},
	{"\xff\xfe", "bytes that cannot start a character"},
	{"\xc0\x80", "an overlong encoding"},
	{"\xf4\x90\x80\x80", "a code point past the last plane"},
	{"\xe4\xb8", "a character cut off mid-encoding"},
	{"\x00\x01\x02", "C0 code points as text"},
	{"\u0085\u009b", "C1 code points as text"},
	{"́", "a combining mark with nothing to attach to"},
	{strings.Repeat("x", 79) + "世́", "a wide base and its mark arriving at the right margin"},
	{"☝️", "a hand with an emoji presentation selector"},
	{"☝︎", "the same hand with a text presentation selector"},
	{"e" + strings.Repeat("́", 40), "a base under forty combining marks"},
	{"‮hello‬", "a right-to-left override run"},
	{"a b", "a non-breaking space mid-string"},
	{"\xed\xa0\x80", "a lone surrogate encoded as CESU-8"},
}

func (g *Gen) text() Seq {
	t := texts[g.u(len(texts))]
	s := t.s
	n := 1
	if g.u(6) == 0 {
		n = 1 + g.u(8)
		s = strings.Repeat(s, n)
	}
	desc := t.name
	if n > 1 {
		desc = fmt.Sprintf("%s x%d", t.name, n)
	}
	return Seq{Kind: "text", Bytes: s, Desc: desc}
}

// sizes are the screens worth resizing to. The degenerate ones are included on
// purpose: a zero or one column screen is what a pane gets when a layout is
// squeezed, and it is where the arithmetic that assumes room stops holding.
var sizes = [][2]int{
	{80, 24}, {80, 10}, {120, 40}, {40, 12}, {20, 5},
	{10, 4}, {6, 4}, {5, 3}, {3, 2}, {2, 2}, {1, 1}, {1, 24}, {80, 1},
	{200, 60}, {2, 100}, {100, 2},
}

func (g *Gen) resize() Seq {
	s := sizes[g.u(len(sizes))]
	g.cols, g.rows = s[0], s[1]
	return Seq{
		Kind: "resize",
		Desc: fmt.Sprintf("resize to %dx%d", s[0], s[1]),
		Cols: s[0], Rows: s[1],
	}
}
