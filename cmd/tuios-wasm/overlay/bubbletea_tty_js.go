//go:build js

package tea

// This file is added to charm.land/bubbletea/v2 at build time through
// `go build -overlay`. Bubble Tea v2.0.8 has TTY and signal files only for
// unix and windows, so a js/wasm build does not compile without these stubs.
// In the browser there is no TTY: input and output are plain io.Reader and
// io.Writer values, and the page sends resizes as WindowSizeMsg.

func (p *Program) initInput() error { return nil }

const suspendSupported = false

func suspendProcess() {}

func (p *Program) listenForResize(done chan struct{}) { close(done) }
