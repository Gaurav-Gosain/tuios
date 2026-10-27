package webshell

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/loom"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

// TML surfaces in the demo: tuios tml show, render and validate, and the tml
// command of tuios.dev/loom. render, validate and tml draw with the real
// libraries here. show hands the document to the surface host, which the
// browser build installs: it opens the surface in the tuios this shell runs
// in, and tells this shell how it closed.

// SurfaceRequest is a tuios tml show, for the surface host.
type SurfaceRequest struct {
	// Doc is the document, checked already.
	Doc []byte
	// Pane opens a tiled pane instead of a popup.
	Pane bool
	// Name is --name, "" for the document's title.
	Name string
	// Width and Height size a popup, as --width and --height give them.
	Width, Height string
	// Window is the pane the command ran in.
	Window string
}

// SurfaceResult is how a surface closed.
type SurfaceResult struct {
	// Reason is "closed" (the person's close action), "dismissed" (closed
	// with no value) or "program".
	Reason string
	// Value is the close value as JSON, "null" when there is none.
	Value json.RawMessage
}

// SurfaceOpened is the host's answer to a SurfaceRequest.
type SurfaceOpened struct {
	Surface   string
	WindowID  string
	Target    string // "popup" or "pane"
	Workspace int
	// Done receives one result, when the surface closes.
	Done <-chan SurfaceResult
}

var (
	surfaceMu   sync.RWMutex
	surfaceHost func(SurfaceRequest) (SurfaceOpened, error)
)

// SetSurfaceHost installs what opens a surface for tuios tml show. Without
// one, show says it needs a real machine.
func SetSurfaceHost(fn func(SurfaceRequest) (SurfaceOpened, error)) {
	surfaceMu.Lock()
	surfaceHost = fn
	surfaceMu.Unlock()
}

// holders are the programs a pane runs that are not commands: nobody types
// them, so help, completion and the launcher leave them out.
var holders = map[string]func(t *TTY) int{
	"tuios-tml": holdTML,
}

// holdTML holds the pane of a TML window, as the daemon's placeholder
// program does. tuios draws the surface over it and keeps the keys, so it
// reads nothing until the pane closes.
func holdTML(t *TTY) int {
	for range t.In {
	}
	return 0
}

// args is a command line split into its files and its flags. Flags may come
// before or after the files, as the real commands take them.
type args struct {
	files []string
	flags map[string]string
}

func (a args) has(name string) bool { _, ok := a.flags[name]; return ok }

func (a args) int(name string) int {
	n, _ := strconv.Atoi(a.flags[name])
	return n
}

// parseArgs reads -x, --x, --x=v and --x v. valued names the flags that
// take a value, bools the ones that do not. Any other flag is an error.
func parseArgs(in []string, valued, bools []string) (args, error) {
	a := args{flags: map[string]string{}}
	for i := 0; i < len(in); i++ {
		arg := in[i]
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			a.files = append(a.files, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch {
		case contains(bools, name):
			a.flags[name] = "true"
		case contains(valued, name):
			if !hasValue {
				if i+1 >= len(in) {
					return a, errors.New("flag needs a value: " + arg)
				}
				i++
				value = in[i]
			}
			a.flags[name] = value
		default:
			return a, errors.New("unknown flag: " + arg)
		}
	}
	return a, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// readSource reads the one file a command takes. It prints why when it
// cannot and returns false.
func (s *shell) readSource(cmd string, a args) (name string, data []byte, ok bool) {
	if len(a.files) != 1 {
		s.fail(cmd + ": give one file, such as " + bold + "deploy.tml" + reset)
		return "", nil, false
	}
	name = a.files[0]
	if name == "-" {
		s.fail(cmd + ": this shell has no pipes. Give a file.")
		return "", nil, false
	}
	content, found := readFile(resolve(s.cwd, name))
	if !found {
		s.fail(cmd + ": open " + name + ": no such file")
		return "", nil, false
	}
	return name, []byte(content), true
}

// checkTML checks a document and prints its problems, as the real commands
// do. It returns the document, or nil when it has errors.
func (s *shell) checkTML(name string, src []byte) *tml.Node {
	lim := tml.PersonLimits()
	if len(src) > lim.DocBytes {
		s.fail(fmt.Sprintf("%s: the document is larger than %d bytes, the limit", name, lim.DocBytes))
		return nil
	}
	doc, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &lim})
	var lines []string
	for _, p := range problems {
		lines = append(lines, name+":"+p.String())
	}
	if err != nil {
		lines = append(lines, red+name+" is not a valid TML document"+reset)
	}
	if len(lines) > 0 {
		s.t.Lay(func(cols int) []string {
			var out []string
			for _, l := range lines {
				out = append(out, wrapWords(l, cols)...)
			}
			return out
		})
	}
	if err != nil {
		return nil
	}
	return doc
}

// cmdTuiosTML is tuios tml.
func cmdTuiosTML(s *shell, in []string) int {
	sub := argOr(in, 0, "")
	rest := []string{}
	if len(in) > 1 {
		rest = in[1:]
	}
	switch sub {
	case "show":
		return tmlShow(s, rest)
	case "render":
		return tmlRender(s, rest)
	case "validate":
		a, err := parseArgs(rest, nil, nil)
		if err != nil {
			return s.fail("tuios tml validate: " + err.Error())
		}
		name, src, ok := s.readSource("tuios tml validate", a)
		if !ok {
			return 1
		}
		if s.checkTML(name, src) == nil {
			return 1
		}
		return 0
	case "", "help", "--help", "-h":
		s.t.Lay(func(cols int) []string {
			lines := wrapWords("Show TML documents: dashboards, results and forms.", cols)
			lines = append(lines, "")
			return append(lines, table([][2]string{
				{"tuios tml show FILE", "open it as a popup"},
				{"tuios tml show FILE --pane", "open it as a pane"},
				{"tuios tml show FILE --wait", "wait, then print the answer"},
				{"tuios tml render FILE", "print it drawn as text"},
				{"tuios tml validate FILE", "check it"},
			}, 2, green, cols)...)
		})
		return 0
	}
	s.t.Print(yellow + "tuios tml " + sub + reset + " needs a real machine. Here, try " + bold + "tuios tml show deploy.tml" + reset + "\r\n")
	return 0
}

// tmlShow is tuios tml show. The surface host opens the surface. With
// --wait it blocks until the surface closes and prints the close value as
// JSON: exit 0 for the person's answer, 1 when they closed it with no value,
// 2 when a program closed it.
func tmlShow(s *shell, in []string) int {
	t := s.t
	a, err := parseArgs(in, []string{"name", "width", "height", "timeout", "session", "s", "workspace"},
		[]string{"wait", "pane", "popup", "json", "events"})
	if err != nil {
		return s.fail("tuios tml show: " + err.Error())
	}
	if a.has("events") {
		t.Print(yellow + "tuios tml show --events" + reset + " needs a real machine, with a script that reads the events.\r\n")
		return 1
	}
	if a.has("pane") && a.has("popup") {
		return s.fail("tuios tml show: --pane and --popup cannot go together")
	}
	if a.has("pane") && (a.has("width") || a.has("height")) {
		return s.fail("tuios tml show: width and height size a popup. A pane takes its tile.")
	}
	name, src, ok := s.readSource("tuios tml show", a)
	if !ok {
		return 1
	}
	if s.checkTML(name, src) == nil {
		return 1
	}
	surfaceMu.RLock()
	host := surfaceHost
	surfaceMu.RUnlock()
	if host == nil {
		t.Print(yellow + "tuios tml show" + reset + " needs a real machine. Try " + bold + "tuios tml render " + name + reset + "\r\n")
		return 0
	}
	opened, err := host(SurfaceRequest{
		Doc: src, Pane: a.has("pane"), Name: a.flags["name"],
		Width: a.flags["width"], Height: a.flags["height"], Window: t.env["TUIOS_WINDOW_ID"],
	})
	if err != nil {
		return s.fail("tuios tml show: " + err.Error())
	}
	if !a.has("wait") {
		if a.has("json") {
			raw, _ := json.Marshal(map[string]any{
				"type": "tml_opened", "surface": opened.Surface, "window_id": opened.WindowID,
				"target": opened.Target, "workspace": opened.Workspace,
			})
			t.Print(string(raw) + "\r\n")
			return 0
		}
		t.Print(opened.Surface + "  " + shortID(opened.WindowID) + "\r\n")
		return 0
	}
	var timeout <-chan time.Time
	if ms := a.int("timeout"); ms > 0 {
		timeout = time.After(time.Duration(ms) * time.Millisecond)
	}
	res, interrupted, late := waitSurface(t, opened.Done, timeout)
	if late {
		return s.fail("tuios tml show: the surface was still open when the wait ended. Close it, or raise --timeout.")
	}
	if interrupted {
		// The real command ends and the surface stays open, as the daemon
		// keeps it.
		t.Print("^C\r\n")
		return 130
	}
	value := res.Value
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	if a.has("json") {
		raw, _ := json.Marshal(map[string]any{
			"type": "tml_result", "surface": opened.Surface, "window_id": opened.WindowID,
			"reason": res.Reason, "value": value,
		})
		t.Print(string(raw) + "\r\n")
	} else {
		t.Print(string(value) + "\r\n")
	}
	switch res.Reason {
	case "dismissed":
		return 1
	case "program":
		return 2
	}
	return 0
}

// waitSurface blocks until done has the result, the person presses ctrl+c
// in this pane, or timeout fires.
func waitSurface(t *TTY, done <-chan SurfaceResult, timeout <-chan time.Time) (res SurfaceResult, interrupted, late bool) {
	for {
		select {
		case res := <-done:
			return res, false, false
		case <-timeout:
			return SurfaceResult{}, false, true
		case chunk, ok := <-t.In:
			if !ok || strings.ContainsRune(string(chunk), 0x03) {
				return SurfaceResult{}, true, false
			}
		}
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// tmlRenderTallest bounds the height render draws at when no height is
// given, as the real command does.
const tmlRenderTallest = 400

// tmlRender is tuios tml render: the document drawn as text, at the pane's
// width unless --width says otherwise.
func tmlRender(s *shell, in []string) int {
	a, err := parseArgs(in, []string{"width", "height", "state", "tab"}, []string{"ansi", "e", "ascii", "popup"})
	if err != nil {
		return s.fail("tuios tml render: " + err.Error())
	}
	name, src, ok := s.readSource("tuios tml render", a)
	if !ok {
		return 1
	}
	doc := s.checkTML(name, src)
	if doc == nil {
		return 1
	}
	opts := tmldraw.Options{
		Height:    a.int("height"),
		Inset:     true,
		Placement: "pane",
		Tab:       a.flags["tab"],
		Styled:    a.has("ansi") || a.has("e"),
		Palette:   tmldraw.Palette(theme.UI(), a.has("ascii")),
	}
	if a.has("popup") {
		opts.Placement = "popup"
	}
	if st := a.flags["state"]; st != "" {
		var probe map[string]any
		if json.Unmarshal([]byte(st), &probe) != nil || probe == nil {
			return s.fail("tuios tml render: --state is not a JSON object: " + st)
		}
		opts.State = json.RawMessage(st)
	}
	natural := opts.Height <= 0
	if natural {
		opts.Height = tmlRenderTallest
	}
	width := a.int("width")
	var drawErr error
	s.t.Lay(func(cols int) []string {
		o := opts
		o.Width = cols
		if width > 0 {
			o.Width = width
		}
		out, err := tmldraw.Draw(doc, o)
		if err != nil {
			drawErr = err
			return wrapWords(red+name+": "+err.Error()+reset, cols)
		}
		lines := strings.Split(out, "\n")
		if natural {
			lines = trimBlankTail(lines)
		}
		return fitLines(lines, cols)
	})
	if drawErr != nil {
		return 1
	}
	return 0
}

// trimBlankTail drops the blank lines at the end but one, the margin under
// the document.
func trimBlankTail(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end < len(lines) {
		end++
	}
	return lines[:end]
}

// cmdTML is the tml command of tuios.dev/loom. A real terminal opens the
// document full screen. Here it prints the document once, as tml --print
// does, and says how to open it in tuios.
func cmdTML(s *shell, in []string, _ string) int {
	a, err := parseArgs(in[1:], []string{"width", "height", "depth"}, []string{"check", "print", "plain", "version", "help", "h"})
	if err != nil {
		return s.fail("tml: " + err.Error())
	}
	if a.has("version") {
		s.t.Print("tml (browser demo)\r\n")
		return 0
	}
	if a.has("help") || a.has("h") || len(a.files) == 0 {
		s.t.Lay(func(cols int) []string {
			lines := wrapWords("Usage: tml [flags] FILE", cols)
			return append(lines, table([][2]string{
				{"tml --check FILE", "check a TML document"},
				{"tml --print FILE", "draw it once, as text"},
			}, 2, green, cols)...)
		})
		return 0
	}
	name, src, ok := s.readSource("tml", a)
	if !ok {
		return 1
	}
	doc := s.checkTML(name, src)
	if doc == nil {
		return 1
	}
	if a.has("check") {
		return 0
	}
	sf, err := loom.New(doc, &loom.Options{Palette: tmldraw.Palette(theme.UI(), a.has("plain")), Ground: loom.GroundNone})
	if err != nil {
		return s.fail("tml: " + err.Error())
	}
	sf.SetPlacement("popup")
	_, rows := s.t.Size()
	width, height := a.int("width"), a.int("height")
	if height <= 0 {
		height = max(rows-1, 1)
	}
	s.t.Lay(func(cols int) []string {
		w := cols
		if width > 0 {
			w = width
		}
		lines := strings.Split(sf.Render(w, height).Render(), "\n")
		lines = trimBlankTail(lines)
		if !a.has("print") {
			lines = append(lines, wrapTokens([]string{dim + "To", "open", "it", "in", "tuios:" + reset, bold + "tuios tml show " + name + reset}, cols)...)
		}
		return fitLines(lines, cols)
	})
	return 0
}
