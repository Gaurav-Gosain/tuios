package session

import (
	"context"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xpty "github.com/charmbracelet/x/xpty"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// debugEnabled returns true if debug logging is enabled via TUIOS_DEBUG_INTERNAL env var
func debugEnabled() bool {
	return os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"
}

// debugLog logs a message only if debug mode is enabled
func debugLog(format string, args ...any) {
	if debugEnabled() {
		log.Printf(format, args...)
	}
}

// WindowState represents the serializable state of a window.
type WindowState struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CustomName   string `json:"custom_name,omitempty"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Z            int    `json:"z"`
	Workspace    int    `json:"workspace"`
	Minimized    bool   `json:"minimized,omitempty"`
	PreMinimizeX int    `json:"pre_minimize_x,omitempty"`
	PreMinimizeY int    `json:"pre_minimize_y,omitempty"`
	PreMinimizeW int    `json:"pre_minimize_w,omitempty"`
	PreMinimizeH int    `json:"pre_minimize_h,omitempty"`
	PTYID        string `json:"pty_id"`                  // Reference to daemon-managed PTY
	IsAltScreen  bool   `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse forwarding)
}

// SerializedBSPNode represents a BSP tree node for serialization
type SerializedBSPNode struct {
	WindowID   int                `json:"window_id"`
	SplitType  int                `json:"split_type"`
	SplitRatio float64            `json:"split_ratio"`
	Left       *SerializedBSPNode `json:"left,omitempty"`
	Right      *SerializedBSPNode `json:"right,omitempty"`
}

// SerializedBSPTree represents a BSP tree for serialization
type SerializedBSPTree struct {
	Root         *SerializedBSPNode `json:"root,omitempty"`
	AutoScheme   int                `json:"auto_scheme"`
	DefaultRatio float64            `json:"default_ratio"`
}

// SessionState represents the complete serializable state of a session.
type SessionState struct {
	Name             string         `json:"name"`
	Windows          []WindowState  `json:"windows"`
	FocusedWindowID  string         `json:"focused_window_id,omitempty"`
	CurrentWorkspace int            `json:"current_workspace"`
	WorkspaceFocus   map[int]string `json:"workspace_focus,omitempty"` // workspace -> focused window ID
	MasterRatio      float64        `json:"master_ratio"`
	AutoTiling       bool           `json:"auto_tiling"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	// Mode: 0 = WindowManagementMode, 1 = TerminalMode
	Mode int `json:"mode"`
	// BSP tiling state
	WorkspaceTrees  map[int]*SerializedBSPTree `json:"workspace_trees,omitempty"`  // BSP tree per workspace
	WindowToBSPID   map[string]int             `json:"window_to_bsp_id,omitempty"` // Window UUID -> BSP int ID
	NextBSPWindowID int                        `json:"next_bsp_window_id,omitempty"`
	TilingScheme    int                        `json:"tiling_scheme,omitempty"` // Default auto-insertion scheme
}

// PTY represents a daemon-managed pseudo-terminal.
type PTY struct {
	ID     string
	pty    xpty.Pty
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc

	// Terminal emulator - maintains scrollback, screen state, cursor position
	// This persists across client disconnect/reconnect
	terminal   *vt.Emulator
	terminalMu sync.RWMutex
	width      int
	height     int

	// Output buffer for reconnection (ring buffer) - legacy, kept for raw output
	outputMu     sync.RWMutex
	outputBuffer []byte
	outputPos    int

	// Subscribers for output streaming
	subscribers   map[string]chan []byte
	subscribersMu sync.RWMutex

	exited   bool
	exitedMu sync.RWMutex
	exitCode int

	// Callback when PTY process exits - used by daemon to notify clients
	onExit func(ptyID string)
}

// Session represents a persistent TUIOS session.
// The daemon manages PTYs and stores state; the client runs the TUI.
type Session struct {
	// Identity
	ID   string
	Name string

	// PTYs managed by this session
	ptys   map[string]*PTY
	ptysMu sync.RWMutex

	// Session state (serializable)
	state   *SessionState
	stateMu sync.RWMutex

	// Terminal size
	width  int
	height int

	// Lifecycle
	Created    time.Time
	LastActive time.Time

	// Configuration
	config *SessionConfig
}

// SessionConfig holds configuration for a session.
type SessionConfig struct {
	Term      string
	ColorTerm string
	Shell     string
}

// NewSession creates a new persistent session.
func NewSession(name string, cfg *SessionConfig, width, height int) (*Session, error) {
	id := uuid.New().String()
	if name == "" {
		name = fmt.Sprintf("session-%s", id[:8])
	}

	now := time.Now()

	session := &Session{
		ID:   id,
		Name: name,
		ptys: make(map[string]*PTY),
		state: &SessionState{
			Name:             name,
			Windows:          []WindowState{},
			CurrentWorkspace: 1,
			WorkspaceFocus:   make(map[int]string),
			MasterRatio:      0.5,
			Width:            width,
			Height:           height,
		},
		width:      width,
		height:     height,
		Created:    now,
		LastActive: now,
		config:     cfg,
	}

	return session, nil
}

// CreatePTY creates a new PTY in this session.
func (s *Session) CreatePTY(width, height int) (*PTY, error) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	shell := s.getShell()

	// Create PTY
	ptyInstance, err := xpty.NewPty(width, height)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create PTY: %w", err)
	}

	// Create command
	cmd := exec.Command(shell)
	cmd.Env = s.buildEnv()

	// Set up the command to use the PTY as controlling terminal
	// This is required for interactive shells to work properly
	// Platform-specific setup is in pty_unix.go and pty_windows.go
	configurePTYCommand(cmd)

	// Start command in PTY
	if err := ptyInstance.Start(cmd); err != nil {
		_ = ptyInstance.Close()
		cancel()
		return nil, fmt.Errorf("failed to start shell: %w", err)
	}

	// Create VT emulator for persistent terminal state
	// This maintains scrollback, screen content, cursor position across reconnects
	terminal := vt.NewEmulator(width, height)
	terminal.SetScrollbackMaxLines(10000) // Match default scrollback

	// Handle Kitty graphics queries on the daemon side for low-latency responses.
	// Query responses go through the same pipe as DA1/CPR, so forwardTerminalResponses()
	// delivers them to the PTY. All other commands (transmit, place, delete) are discarded
	// here — the client's passthrough handles those and forwards to the host terminal.
	terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		if cmd.Action == vt.KittyActionQuery {
			response := vt.BuildKittyResponse(true, cmd.ImageID, "")
			terminal.WriteResponse(response)
			return
		}
		debugLog("[DAEMON-VT] Kitty command discarded: action=%c, imageID=%d", cmd.Action, cmd.ImageID)
	})

	pty := &PTY{
		ID:           id,
		pty:          ptyInstance,
		cmd:          cmd,
		ctx:          ctx,
		cancel:       cancel,
		terminal:     terminal,
		width:        width,
		height:       height,
		outputBuffer: make([]byte, 64*1024), // 64KB ring buffer
		subscribers:  make(map[string]chan []byte),
	}

	s.ptys[id] = pty

	// Start output reader
	go pty.readOutput()

	// Start terminal response forwarder - the daemon's emulator generates query responses
	// (DA, CPR, etc.) which must be sent to the PTY for applications to receive.
	// Client emulators DRAIN their responses to prevent duplicates.
	go pty.forwardTerminalResponses()

	// Monitor process exit
	go pty.monitorExit()

	s.LastActive = time.Now()
	return pty, nil
}

// GetPTY returns a PTY by ID.
func (s *Session) GetPTY(id string) *PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return s.ptys[id]
}

// ClosePTY closes and removes a PTY.
func (s *Session) ClosePTY(id string) error {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	pty, exists := s.ptys[id]
	if !exists {
		return fmt.Errorf("PTY %s not found", id)
	}

	delete(s.ptys, id)
	return pty.Close()
}

// ListPTYIDs returns all PTY IDs in this session.
func (s *Session) ListPTYIDs() []string {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()

	ids := make([]string, 0, len(s.ptys))
	for id := range s.ptys {
		ids = append(ids, id)
	}
	return ids
}

// PTYCount returns the number of PTYs.
func (s *Session) PTYCount() int {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return len(s.ptys)
}

// GetState returns the current session state.
func (s *Session) GetState() *SessionState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	// Return a copy
	stateCopy := *s.state
	stateCopy.Windows = make([]WindowState, len(s.state.Windows))
	copy(stateCopy.Windows, s.state.Windows)
	if s.state.WorkspaceFocus != nil {
		stateCopy.WorkspaceFocus = make(map[int]string)
		maps.Copy(stateCopy.WorkspaceFocus, s.state.WorkspaceFocus)
	}
	return &stateCopy
}

// UpdateState updates the session state.
func (s *Session) UpdateState(state *SessionState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.state = state
	s.LastActive = time.Now()
}

// Stop closes all PTYs and cleans up.
func (s *Session) Stop() {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	for id, pty := range s.ptys {
		_ = pty.Close()
		delete(s.ptys, id)
	}
}

// WindowCount returns the number of windows in state.
func (s *Session) WindowCount() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.state.Windows)
}

// Size returns the current session dimensions.
func (s *Session) Size() (width, height int) {
	return s.width, s.height
}

// Resize updates the session dimensions.
// This is called when the effective size changes (min of all connected clients).
func (s *Session) Resize(width, height int) {
	s.width = width
	s.height = height

	// Resize all PTYs to match the new session size
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	for _, pty := range s.ptys {
		_ = pty.Resize(width, height) // Best effort resize
	}
}

// Info returns session information.
func (s *Session) Info() SessionInfo {
	return SessionInfo{
		Name:        s.Name,
		ID:          s.ID,
		Created:     s.Created.Unix(),
		LastActive:  s.LastActive.Unix(),
		WindowCount: s.WindowCount(),
		Attached:    false, // Will be set by manager
		Width:       s.width,
		Height:      s.height,
	}
}

func (s *Session) getShell() string {
	if s.config != nil && s.config.Shell != "" {
		return s.config.Shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/sh"
}

func (s *Session) buildEnv() []string {
	env := os.Environ()

	term := "xterm-256color"
	if s.config != nil && s.config.Term != "" {
		term = s.config.Term
	}
	env = append(env, "TERM="+term)

	colorTerm := "truecolor"
	if s.config != nil && s.config.ColorTerm != "" {
		colorTerm = s.config.ColorTerm
	}
	env = append(env, "COLORTERM="+colorTerm)
	env = append(env, "TERM_PROGRAM=TUIOS")
	env = append(env, "TERM_PROGRAM_VERSION=0.1.0")
	env = append(env, "TUIOS_SESSION="+s.Name)

	return env
}

// PTY methods

// Subscribe adds a subscriber to receive PTY output.
func (p *PTY) Subscribe(clientID string) <-chan []byte {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	// Return existing channel if already subscribed
	if existing, ok := p.subscribers[clientID]; ok {
		debugLog("[DEBUG] PTY %s: client %s already subscribed", p.ID[:8], clientID)
		return existing
	}

	ch := make(chan []byte, 256)
	p.subscribers[clientID] = ch
	debugLog("[DEBUG] PTY %s: added subscriber %s (total: %d)", p.ID[:8], clientID, len(p.subscribers))

	// Send buffered output to catch up
	p.outputMu.RLock()
	if p.outputPos > 0 {
		debugLog("[DEBUG] PTY %s: sending %d buffered bytes to new subscriber", p.ID[:8], p.outputPos)
		bufCopy := make([]byte, p.outputPos)
		copy(bufCopy, p.outputBuffer[:p.outputPos])
		select {
		case ch <- bufCopy:
			debugLog("[DEBUG] PTY %s: buffered output sent", p.ID[:8])
		default:
			debugLog("[DEBUG] PTY %s: failed to send buffered output (channel full)", p.ID[:8])
		}
	} else {
		debugLog("[DEBUG] PTY %s: no buffered output to send", p.ID[:8])
	}
	p.outputMu.RUnlock()

	return ch
}

// Unsubscribe removes a subscriber.
func (p *PTY) Unsubscribe(clientID string) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	if ch, ok := p.subscribers[clientID]; ok {
		close(ch)
		delete(p.subscribers, clientID)
	}
}

// Write sends input to the PTY.
func (p *PTY) Write(data []byte) (int, error) {
	if p.pty == nil {
		return 0, fmt.Errorf("PTY not available")
	}
	return p.pty.Write(data)
}

// Size returns the current PTY dimensions.
func (p *PTY) Size() (width, height int) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.width, p.height
}

// SetCellSize sets the cell dimensions in pixels for the PTY's VT emulator.
// This enables proper XTWINOPS responses (CSI 14t, CSI 16t) for applications
// that query terminal pixel dimensions.
func (p *PTY) SetCellSize(cellWidth, cellHeight int) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil && cellWidth > 0 && cellHeight > 0 {
		p.terminal.SetCellSize(cellWidth, cellHeight)
	}
}

// UpdatePixelDimensions sets the cell size on the VT emulator and updates the PTY's
// pixel dimensions based on the current terminal size and the given cell dimensions.
// This is a convenience method that combines SetCellSize and SetPixelSize.
func (p *PTY) UpdatePixelDimensions(cellWidth, cellHeight int) error {
	if cellWidth <= 0 || cellHeight <= 0 {
		return nil
	}
	p.SetCellSize(cellWidth, cellHeight)
	width, height := p.Size()
	return p.SetPixelSize(width, height, width*cellWidth, height*cellHeight)
}

// Resize changes the PTY and terminal emulator size.
func (p *PTY) Resize(width, height int) error {
	// Resize VT emulator
	p.terminalMu.Lock()
	if p.terminal != nil {
		p.terminal.Resize(width, height)
	}
	p.width = width
	p.height = height
	p.terminalMu.Unlock()

	// Resize PTY
	if p.pty != nil {
		return p.pty.Resize(width, height)
	}
	return nil
}

// GetTerminalState returns the current terminal screen state for restore.
// Returns the visible screen content as a 2D array of cells.
func (p *PTY) GetTerminalState() *TerminalState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()

	if p.terminal == nil {
		return nil
	}

	state := &TerminalState{
		Width:         p.width,
		Height:        p.height,
		CursorX:       p.terminal.CursorPosition().X,
		CursorY:       p.terminal.CursorPosition().Y,
		ScrollbackLen: p.terminal.ScrollbackLen(),
		IsAltScreen:   p.terminal.IsAltScreen(), // Capture alt screen state for mouse event forwarding
		Modes:         p.terminal.GetModes(),    // Capture terminal modes (mouse tracking, bracketed paste, etc.)
		Screen:        make([][]CellState, p.height),
		Scrollback:    make([][]CellState, 0),
	}

	// Capture visible screen with full styling
	for y := 0; y < p.height; y++ {
		state.Screen[y] = make([]CellState, p.width)
		for x := 0; x < p.width; x++ {
			cell := p.terminal.CellAt(x, y)
			if cell != nil {
				state.Screen[y][x] = cellToState(cell)
			}
		}
	}

	// Capture scrollback (up to a reasonable limit)
	scrollbackLen := p.terminal.ScrollbackLen()
	maxScrollback := 1000 // Limit for initial sync
	if scrollbackLen > maxScrollback {
		scrollbackLen = maxScrollback
	}

	for i := 0; i < scrollbackLen; i++ {
		line := p.terminal.ScrollbackLine(i)
		if line != nil {
			row := make([]CellState, len(line))
			for x, cell := range line {
				row[x] = cellToState(&cell)
			}
			state.Scrollback = append(state.Scrollback, row)
		}
	}

	return state
}

// TerminalState represents the serializable state of a terminal.
type TerminalState struct {
	Width         int           `json:"width"`
	Height        int           `json:"height"`
	CursorX       int           `json:"cursor_x"`
	CursorY       int           `json:"cursor_y"`
	ScrollbackLen int           `json:"scrollback_len"`
	IsAltScreen   bool          `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse event forwarding)
	Modes         map[int]bool  `json:"modes,omitempty"`         // Terminal modes (mouse tracking, bracketed paste, etc.)
	Screen        [][]CellState `json:"screen"`
	Scrollback    [][]CellState `json:"scrollback,omitempty"`
}

// CellState represents a single terminal cell with full styling information.
type CellState struct {
	Content   string `json:"c,omitempty"`  // Cell content (character or grapheme)
	Width     int    `json:"w,omitempty"`  // Cell width (1 for normal, 2 for wide chars, 0 for continuation)
	FgColor   string `json:"fg,omitempty"` // Foreground color (hex format like "#ff0000" or empty for default)
	BgColor   string `json:"bg,omitempty"` // Background color (hex format or empty)
	Bold      bool   `json:"b,omitempty"`  // Bold attribute
	Italic    bool   `json:"i,omitempty"`  // Italic attribute
	Underline bool   `json:"u,omitempty"`  // Underline attribute
	Reverse   bool   `json:"r,omitempty"`  // Reverse video attribute
	Blink     bool   `json:"bl,omitempty"` // Blink attribute
	Faint     bool   `json:"f,omitempty"`  // Faint/dim attribute
}

// cellToState converts a VT cell to a serializable CellState.
func cellToState(cell *uv.Cell) CellState {
	if cell == nil {
		return CellState{}
	}

	cs := CellState{
		Content: cell.Content,
		Width:   cell.Width,
	}

	// Convert colors to hex strings for JSON serialization
	if cell.Style.Fg != nil {
		r, g, b, _ := cell.Style.Fg.RGBA()
		cs.FgColor = fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	if cell.Style.Bg != nil {
		r, g, b, _ := cell.Style.Bg.RGBA()
		cs.BgColor = fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}

	// Copy style attributes from bitmask
	// Attrs bitmask using uv.Attr* constants
	cs.Bold = cell.Style.Attrs&uv.AttrBold != 0
	cs.Faint = cell.Style.Attrs&uv.AttrFaint != 0
	cs.Italic = cell.Style.Attrs&uv.AttrItalic != 0
	cs.Reverse = cell.Style.Attrs&uv.AttrReverse != 0
	cs.Underline = cell.Style.Underline != ansi.UnderlineNone // Any underline style (single, double, curly, etc.)
	// Note: Blink not commonly used in modern terminals, omitting for now

	return cs
}

// StateToCell converts a CellState back to a VT cell for restoration.
func StateToCell(cs CellState) *uv.Cell {
	cell := &uv.Cell{
		Content: cs.Content,
		Width:   cs.Width,
	}

	// Parse color strings back to color.Color using ansi.RGBColor
	if cs.FgColor != "" {
		var r, g, b uint8
		if _, err := fmt.Sscanf(cs.FgColor, "#%02x%02x%02x", &r, &g, &b); err == nil {
			cell.Style.Fg = ansi.RGBColor{R: r, G: g, B: b}
		}
	}
	if cs.BgColor != "" {
		var r, g, b uint8
		if _, err := fmt.Sscanf(cs.BgColor, "#%02x%02x%02x", &r, &g, &b); err == nil {
			cell.Style.Bg = ansi.RGBColor{R: r, G: g, B: b}
		}
	}

	// Restore style attributes using direct field assignment
	if cs.Bold {
		cell.Style.Attrs |= uv.AttrBold
	}
	if cs.Faint {
		cell.Style.Attrs |= uv.AttrFaint
	}
	if cs.Italic {
		cell.Style.Attrs |= uv.AttrItalic
	}
	if cs.Reverse {
		cell.Style.Attrs |= uv.AttrReverse
	}
	if cs.Underline {
		cell.Style.Underline = ansi.UnderlineSingle
	}

	return cell
}

// Close terminates the PTY.
func (p *PTY) Close() error {
	p.cancel()

	// Close all subscriber channels
	p.subscribersMu.Lock()
	for id, ch := range p.subscribers {
		close(ch)
		delete(p.subscribers, id)
	}
	p.subscribersMu.Unlock()

	// Kill process
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}

	// Close PTY
	if p.pty != nil {
		return p.pty.Close()
	}
	return nil
}

// IsExited returns true if the shell process has exited.
func (p *PTY) IsExited() bool {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exited
}

func (p *PTY) readOutput() {
	debugLog("[DEBUG] PTY %s: readOutput started", p.ID[:8])
	buf := make([]byte, 4096)
	for {
		select {
		case <-p.ctx.Done():
			debugLog("[DEBUG] PTY %s: context done, stopping readOutput", p.ID[:8])
			return
		default:
		}

		n, err := p.pty.Read(buf)
		if err != nil {
			if err != io.EOF {
				debugLog("[DEBUG] PTY %s: read error: %v", p.ID[:8], err)
			} else {
				debugLog("[DEBUG] PTY %s: EOF", p.ID[:8])
			}
			return
		}

		if n > 0 {
			debugLog("[DEBUG] PTY %s: read %d bytes, about to process", p.ID[:8], n)
			data := make([]byte, n)
			copy(data, buf[:n])

			// Write to VT emulator for persistent terminal state
			// This maintains scrollback and screen content across reconnects
			// NOTE: Do this in a separate goroutine to avoid blocking output
			go func(termData []byte) {
				p.terminalMu.Lock()
				if p.terminal != nil {
					_, _ = p.terminal.Write(termData)
				}
				p.terminalMu.Unlock()
			}(data)

			// Store in ring buffer
			p.outputMu.Lock()
			p.appendToBuffer(data)
			p.outputMu.Unlock()

			// Broadcast to subscribers
			debugLog("[DEBUG] PTY %s: calling broadcast with %d bytes", p.ID[:8], len(data))
			p.broadcast(data)
		}
	}
}

func (p *PTY) appendToBuffer(data []byte) {
	space := len(p.outputBuffer) - p.outputPos
	if len(data) > space {
		// Shift buffer, keep last half
		half := len(p.outputBuffer) / 2
		copy(p.outputBuffer, p.outputBuffer[half:p.outputPos])
		p.outputPos = p.outputPos - half
	}
	copy(p.outputBuffer[p.outputPos:], data)
	p.outputPos += len(data)
}

func (p *PTY) broadcast(data []byte) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()

	debugLog("[DEBUG] PTY %s: BROADCAST called with %d bytes, %d subscribers", p.ID[:8], len(data), len(p.subscribers))
	for clientID, ch := range p.subscribers {
		select {
		case ch <- data:
			debugLog("[DEBUG] PTY %s: sent to %s", p.ID[:8], clientID)
		default:
			debugLog("[DEBUG] PTY %s: channel full for %s, dropped", p.ID[:8], clientID)
		}
	}
}

func (p *PTY) monitorExit() {
	if p.cmd == nil {
		return
	}

	_ = p.cmd.Wait()

	p.exitedMu.Lock()
	p.exited = true
	if p.cmd.ProcessState != nil {
		p.exitCode = p.cmd.ProcessState.ExitCode()
	}
	p.exitedMu.Unlock()

	debugLog("[DEBUG] PTY %s: process exited with code %d", p.ID[:8], p.exitCode)

	// Notify callback (used by daemon to inform clients)
	if p.onExit != nil {
		p.onExit(p.ID)
	}
}

// SetOnExit sets the callback to be called when the PTY process exits.
func (p *PTY) SetOnExit(callback func(ptyID string)) {
	p.onExit = callback
}

// forwardTerminalResponses reads responses from the daemon's terminal emulator and
// forwards them to the PTY as input for applications to receive.
// The emulator writes responses (like DA1, CPR) to its pipe. If nothing reads from the pipe,
// Write() will block forever (io.Pipe is synchronous).
// Client emulators DRAIN their responses to prevent duplicates.
func (p *PTY) forwardTerminalResponses() {
	if p.terminal == nil {
		return
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
			n, err := p.terminal.Read(buf)
			if err != nil {
				return
			}
			if n > 0 && p.pty != nil {
				// Forward response to PTY as input
				_, _ = p.pty.Write(buf[:n])
			}
		}
	}
}
