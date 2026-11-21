package vt

import (
	"encoding/hex"
	"image/color"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DefaultTerminalName is the program name XTVERSION reports until
// SetTerminalName sets another.
const DefaultTerminalName = "vt"

var (
	terminalName atomic.Pointer[string]
	buildVersion atomic.Pointer[string]
)

// SetTerminalName sets the program name XTVERSION reports. An empty name
// restores DefaultTerminalName. Call it once, before the first pane starts.
func SetTerminalName(name string) {
	name = strings.TrimSpace(name)
	terminalName.Store(&name)
}

// SetBuildVersion records the version XTVERSION reports after the program
// name. Call it once, before the first pane starts.
func SetBuildVersion(v string) {
	v = strings.TrimSpace(v)
	buildVersion.Store(&v)
}

// XTVersionName is the text an XTVERSION reply carries: the program name,
// followed by the build version when one was set. Both backends answer with
// it, so a guest that picks its features by terminal name sees the same name
// on either.
func XTVersionName() string {
	name := DefaultTerminalName
	if n := terminalName.Load(); n != nil && *n != "" {
		name = *n
	}
	v := buildVersion.Load()
	if v == nil || *v == "" {
		return name
	}
	return name + " " + *v
}

const unitID = "00000000"

func (e *Emulator) registerReportHandlers() {
	e.RegisterCsiHandler(ansi.Command('>', 0, 'q'), func(params ansi.Params) bool {
		if n, _, _ := params.Param(0, 0); n != 0 {
			return false
		}
		_, _ = io.WriteString(e.pipe, "\x1bP>|"+XTVersionName()+"\x1b\\")
		return true
	})

	e.RegisterCsiHandler(ansi.Command('=', 0, 'c'), func(params ansi.Params) bool {
		if n, _, _ := params.Param(0, 0); n != 0 {
			return false
		}
		_, _ = io.WriteString(e.pipe, "\x1bP!|"+unitID+"\x1b\\")
		return true
	})

	e.RegisterDcsHandler(ansi.Command(0, '+', 'q'), func(_ ansi.Params, data []byte) bool {
		_, _ = io.WriteString(e.pipe, termcapReply(string(data)))
		return true
	})
}

type termcapValue struct {
	value   string
	boolean bool
}

var termcaps = map[string]termcapValue{
	"TN":      {value: "xterm-256color"},
	"colors":  {value: "256"},
	"Co":      {value: "256"},
	"RGB":     {boolean: true},
	"Tc":      {boolean: true},
	"setrgbf": {value: "\x1b[38;2;%p1%d;%p2%d;%p3%dm"},
	"setrgbb": {value: "\x1b[48;2;%p1%d;%p2%d;%p3%dm"},
	"Su":      {boolean: true},
	"Smulx":   {value: "\x1b[4:%p1%dm"},
	"Setulc":  {value: "\x1b[58:2:%p1%{65536}%/%d:%p1%{256}%/%{255}%&%d:%p1%{255}%&%d%;m"},
	"Ms":      {value: "\x1b]52;%p1%s;%p2%s\x07"},
	"Ss":      {value: "\x1b[%p1%d q"},
	"Se":      {value: "\x1b[2 q"},
	"Cs":      {value: "\x1b]12;%p1%s\x07"},
	"Cr":      {value: "\x1b]112\x07"},
	"Sync":    {value: "\x1b[?2026%?%p1%{1}%-%tl%eh%;"},
	"BE":      {value: "\x1b[?2004h"},
	"BD":      {value: "\x1b[?2004l"},
	"PS":      {value: "\x1b[200~"},
	"PE":      {value: "\x1b[201~"},
	"fe":      {value: "\x1b[?1004h"},
	"fd":      {value: "\x1b[?1004l"},
	"smxx":    {value: "\x1b[9m"},
	"rmxx":    {value: "\x1b[29m"},
}

// AddTermcapBoolean makes XTGETTCAP answer a boolean capability the embedder
// implements itself. Call it before the first pane starts.
func AddTermcapBoolean(name string) {
	termcaps[name] = termcapValue{boolean: true}
}

func termcapReply(req string) string {
	var b strings.Builder
	for name := range strings.SplitSeq(req, ";") {
		raw, err := hex.DecodeString(name)
		cap, ok := termcaps[string(raw)]
		if err != nil || name == "" || !ok {
			b.WriteString("\x1bP0+r\x1b\\")
			continue
		}
		b.WriteString("\x1bP1+r")
		b.WriteString(name)
		if !cap.boolean {
			b.WriteByte('=')
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte(cap.value))))
		}
		b.WriteString("\x1b\\")
	}
	return b.String()
}

func sgrReport(pen uv.Style) string {
	var b strings.Builder
	b.WriteByte('0')
	param := func(s string) {
		b.WriteByte(';')
		b.WriteString(s)
	}
	attr := func(bit uint8, code string) {
		if pen.Attrs&bit != 0 {
			param(code)
		}
	}
	attr(uv.AttrBold, "1")
	attr(uv.AttrFaint, "2")
	attr(uv.AttrItalic, "3")
	switch pen.Underline {
	case ansi.UnderlineNone:
	case ansi.UnderlineSingle:
		param("4")
	default:
		param("4:" + strconv.Itoa(int(pen.Underline)))
	}
	attr(uv.AttrBlink, "5")
	attr(uv.AttrRapidBlink, "6")
	attr(uv.AttrReverse, "7")
	attr(uv.AttrConceal, "8")
	attr(uv.AttrStrikethrough, "9")
	if pen.Fg != nil {
		param(sgrColor(pen.Fg, 30, 90, 38))
	}
	if pen.Bg != nil {
		param(sgrColor(pen.Bg, 40, 100, 48))
	}
	if pen.UnderlineColor != nil {
		param(sgrColor(pen.UnderlineColor, -1, -1, 58))
	}
	return b.String()
}

func sgrColor(c color.Color, base, bright, ext int) string {
	e := strconv.Itoa(ext)
	switch c := c.(type) {
	case ansi.BasicColor:
		switch {
		case base >= 0 && c < 8:
			return strconv.Itoa(base + int(c))
		case bright >= 0 && c < 16:
			return strconv.Itoa(bright + int(c) - 8)
		default:
			return e + ";5;" + strconv.Itoa(int(c))
		}
	case ansi.IndexedColor:
		return e + ";5;" + strconv.Itoa(int(c))
	}
	r, g, bl, _ := c.RGBA()
	return e + ";2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(bl>>8))
}

func cursorStyleReport(style CursorStyle, steady bool) int {
	n := int(style)*2 + 1
	if steady {
		n++
	}
	return n
}
