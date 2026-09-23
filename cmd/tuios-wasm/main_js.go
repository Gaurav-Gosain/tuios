//go:build js && wasm

// Command tuios-wasm runs tuios entirely in a browser tab, for the guided tour
// at tuios.gaurav.zip/learn. There is no server and no daemon: panes run the
// in-memory fake shell from internal/webshell, the page owns the terminal
// renderer, and internal/learn reports what the person does.
//
// The page talks to it through one global object, `tuios`, which the program
// sets before it starts. README.md documents it in full.
package main

import (
	"bytes"
	"os"
	"sync"
	"syscall/js"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// jsBridge holds the callbacks the page registered. Output and events that
// arrive before a callback is registered are held and delivered to it.
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

	cols, rows := 120, 36
	if v := js.Global().Get("tuiosInitialSize"); v.Truthy() {
		cols, rows = v.Index(0).Int(), v.Index(1).Int()
	}

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
		LearnMode:       true,
		GuestApps:       learn.LauncherApps(),
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

	var prog *tea.Program
	send := func(msg tea.Msg) {
		// A js.Func must not block, and Send blocks until the program's loop
		// takes the message.
		go prog.Send(msg)
	}
	model := learn.New(osModel, func(e learn.Event) { bridge.emit(e.ToMap()) }, send)

	inputPipe := newInputPipe()
	// The transport's own options: the page's input, the output writer, and
	// the size, the three things sip's MakeOptions supplies for a served
	// session. The size has to be an option: with no TTY to ask, Bubble Tea
	// otherwise starts at 0x0 and reports that after any WindowSizeMsg sent
	// before Run.
	opts := append(app.ProgramOptions(),
		tea.WithInput(inputPipe),
		tea.WithOutput(bridge),
		tea.WithWindowSize(cols, rows),
		// Last, so it replaces the filter ProgramOptions set: that one only
		// recognises *app.OS, and the program runs the tour's model. This one
		// unwraps it, turns a quit into a note, and runs the same motion
		// filter.
		tea.WithFilter(learn.Filter),
	)
	prog = tea.NewProgram(model, opts...)

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
			send(tea.WindowSizeMsg{Width: c, Height: r})
		}
		return nil
	}))
	api.Set("state", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return js.ValueOf(model.State().ToMap())
	}))
	api.Set("command", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		strs := make([]string, len(args))
		for i, a := range args {
			strs[i] = a.String()
		}
		send(learn.CommandMsg{Name: strs[0], Args: strs[1:]})
		return nil
	}))
	api.Set("actions", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return js.ValueOf(stringMap(learn.Actions()))
	}))
	api.Set("unavailable", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return js.ValueOf(stringMap(app.LearnUnavailableActions()))
	}))
	api.Set("commands", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		out := make([]any, len(learn.Commands))
		for i, c := range learn.Commands {
			out[i] = c
		}
		return js.ValueOf(out)
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

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
