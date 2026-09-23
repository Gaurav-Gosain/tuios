//go:build js && wasm

// Command tuios-wasm runs tuios entirely in a browser tab. There is no server
// and no daemon: panes run the in-memory fake shell from internal/webshell, and
// the page owns the terminal renderer.
//
// The page talks to it through one global object, `tuios`, which the program
// sets before it starts:
//
//	tuios.onOutput(fn)   fn(Uint8Array): bytes for the terminal renderer
//	tuios.onEvent(fn)    fn(object): what the user did, for a guided tour
//	tuios.input(data)    keyboard and mouse bytes, a string or a Uint8Array
//	tuios.resize(c, r)   the renderer's size in cells
//	tuios.state()        the current state as an object
//	tuios.command(name, ...args)  drive the app from the page (see runCommand)
//
// Output written before onOutput is registered is held and delivered to the
// first callback, so the page can register after start.
package main

import (
	"bytes"
	"os"
	"sync"
	"syscall/js"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// jsBridge holds the callbacks the page registered.
type jsBridge struct {
	mu       sync.Mutex
	output   js.Value
	event    js.Value
	pending  [][]byte
	pendingE []map[string]any
}

var bridge = &jsBridge{}

// Write sends terminal output to the page.
//
// Bubble Tea's renderer runs with no TTY here, and with no TTY it assumes the
// output is cooked: that a bare LF comes out as CR LF, which is what the
// kernel's ONLCR does on a real pty. There is no pty between it and the
// renderer in the page, so the mapping is done here. Without it every LF the
// renderer emits leaves the cursor in its column, and the first one at the
// bottom row scrolls the whole screen up a line.
func (b *jsBridge) Write(p []byte) (int, error) {
	n := len(p)
	p = mapNewlines(p)
	b.mu.Lock()
	fn := b.output
	if fn.IsUndefined() || fn.IsNull() {
		b.pending = append(b.pending, p)
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	b.send(fn, p)
	return n, nil
}

// mapNewlines turns every LF not already preceded by CR into CR LF, which is
// what ONLCR does. The result is always a copy, since the caller's buffer may
// be reused after Write returns.
func mapNewlines(p []byte) []byte {
	out := make([]byte, 0, len(p)+bytes.Count(p, []byte("\n")))
	for i, c := range p {
		if c == '\n' && (i == 0 || p[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	return out
}

func (b *jsBridge) send(fn js.Value, p []byte) {
	arr := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(arr, p)
	fn.Invoke(arr)
}

func (b *jsBridge) emit(ev map[string]any) {
	b.mu.Lock()
	fn := b.event
	if fn.IsUndefined() || fn.IsNull() {
		b.pendingE = append(b.pendingE, ev)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	fn.Invoke(js.ValueOf(ev))
}

func main() {
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("COLORTERM", "truecolor")
	// Colour detection says "no TTY, no colour" for a writer that is not a
	// terminal unless this is set.
	_ = os.Setenv("CLICOLOR_FORCE", "1")
	_ = os.Setenv("HOME", webshell.Home)
	_ = os.Setenv("USER", "guest")
	_ = os.Setenv("SHELL", "/bin/sh")

	// Every pane gets an in-memory pty running the fake shell.
	ptyspawn.NewGuestPty = func(width, height int) (xpty.Pty, error) {
		return webshell.NewPty(width, height), nil
	}
	webshell.SetEventSink(func(e webshell.Event) {
		ev := map[string]any{"type": e.Type}
		if e.WindowID != "" {
			ev["windowId"] = e.WindowID
		}
		data := map[string]any{}
		for k, v := range e.Data {
			data[k] = v
		}
		ev["data"] = data
		bridge.emit(ev)
	})

	cols, rows := 120, 36
	if v := js.Global().Get("tuiosInitialSize"); v.Truthy() {
		cols, rows = v.Index(0).Int(), v.Index(1).Int()
	}

	// The compositor serializes its canvas with lipgloss.Sprint, which
	// downsamples to lipgloss.Writer's profile. That profile is detected from
	// os.Stdout, which in the browser is not a terminal, so every colour was
	// stripped from any frame the compositor drew (two panes, an overlay, the
	// prefix). The page's renderer is truecolor.
	lipgloss.Writer.Profile = colorprofile.TrueColor

	cfg := config.DefaultConfig()
	// A browser tab has no desktop notifications. Left on, the default warns
	// about it at startup, which is noise in a tutorial.
	noNotify := false
	cfg.Notifications.Agent.Notify = &noNotify
	app.SetInputHandler(input.HandleInput)
	config.ApplyAppearanceConfig(cfg, &config.Global)
	seed := config.AppearanceFrom(cfg, config.Overrides{})
	osModel := app.NewOS(app.OSOptions{
		Client:          app.ClientBrowser,
		ConfigReadOnly:  true,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		Settings:        &seed,
		Width:           cols,
		Height:          rows,
		GraphicsOutput:  bridge,
		Caps: &app.HostCapabilities{
			TrueColor:    true,
			TerminalName: "tuios-wasm",
		},
	})

	inputPipe := newInputPipe()
	model := newObserved(osModel, bridge.emit)
	// The transport's own options: the page's input, the output writer, and
	// the size, the three things sip's MakeOptions supplies for a served
	// session. Environment and colour profile need no option: the environment
	// set above (with CLICOLOR_FORCE) makes Bubble Tea detect truecolor on a
	// writer that is not a terminal. The size has to be an option: with no
	// TTY to ask, Bubble Tea otherwise starts at 0x0 and reports that after
	// any WindowSizeMsg sent before Run.
	opts := append(app.ProgramOptions(),
		tea.WithInput(inputPipe),
		tea.WithOutput(bridge),
		tea.WithWindowSize(cols, rows),
		// Last, so it replaces the filter ProgramOptions set: that one only
		// recognises *app.OS, and the program runs the observing wrapper.
		tea.WithFilter(func(m tea.Model, msg tea.Msg) tea.Msg {
			if o, ok := m.(*observed); ok {
				return app.FilterMouseMotion(o.OS, msg)
			}
			return msg
		}),
	)
	prog := tea.NewProgram(model, opts...)

	api := js.Global().Get("Object").New()
	api.Set("onOutput", js.FuncOf(func(_ js.Value, args []js.Value) any {
		bridge.mu.Lock()
		bridge.output = args[0]
		pending := bridge.pending
		bridge.pending = nil
		bridge.mu.Unlock()
		for _, p := range pending {
			bridge.send(args[0], p)
		}
		return nil
	}))
	api.Set("onEvent", js.FuncOf(func(_ js.Value, args []js.Value) any {
		bridge.mu.Lock()
		bridge.event = args[0]
		pending := bridge.pendingE
		bridge.pendingE = nil
		bridge.mu.Unlock()
		for _, e := range pending {
			bridge.emit(e)
		}
		return nil
	}))
	api.Set("input", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		v := args[0]
		if v.Type() == js.TypeString {
			inputPipe.write([]byte(v.String()))
			return nil
		}
		buf := make([]byte, v.Get("length").Int())
		js.CopyBytesToGo(buf, v)
		inputPipe.write(buf)
		return nil
	}))
	api.Set("resize", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 2 {
			return nil
		}
		c, r := args[0].Int(), args[1].Int()
		if c > 0 && r > 0 {
			// A js.Func must not block, and Send blocks until the program's
			// loop takes the message, which it cannot do before Run.
			go prog.Send(tea.WindowSizeMsg{Width: c, Height: r})
		}
		return nil
	}))
	api.Set("state", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return js.ValueOf(model.lastState().toJS())
	}))
	api.Set("command", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		strs := make([]string, len(args))
		for i, a := range args {
			strs[i] = a.String()
		}
		go prog.Send(commandMsg{name: strs[0], args: strs[1:]})
		return nil
	}))
	js.Global().Set("tuios", api)
	if ready := js.Global().Get("onTuiosReady"); ready.Type() == js.TypeFunction {
		ready.Invoke(api)
	}

	if _, err := prog.Run(); err != nil {
		bridge.emit(map[string]any{"type": "exit", "data": map[string]any{"error": err.Error()}})
		return
	}
	bridge.emit(map[string]any{"type": "exit", "data": map[string]any{}})
}

// inputPipe is what Bubble Tea reads the page's input from.
type inputPipe struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
}

func newInputPipe() *inputPipe {
	p := &inputPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *inputPipe) write(b []byte) {
	p.mu.Lock()
	p.buf = append(p.buf, b...)
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *inputPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 {
		p.cond.Wait()
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}
