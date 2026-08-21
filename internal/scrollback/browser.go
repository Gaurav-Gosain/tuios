package scrollback

import "strings"

// BrowserMode selects what the browser displays.
type BrowserMode int

const (
	// ModeCommands shows command/output blocks.
	ModeCommands BrowserMode = iota
	// ModeJSON shows extracted JSON fragments.
	ModeJSON
	// ModePaths shows extracted file paths and URLs.
	ModePaths
)

// Browser holds the state for the scrollback browser overlay.
type Browser struct {
	Blocks        []CommandBlock
	FilteredIdx   []int // indices into Blocks matching search
	SelectedIdx   int   // cursor in filtered list
	ScrollOffset  int   // left pane scroll
	PreviewScroll int   // right pane scroll

	Mode         BrowserMode
	SearchQuery  string
	SearchActive bool
	MultiSelect  map[int]bool // selected block indices (for multi-yank)

	// Diagnostic info (displayed in header)
	ParseMethod string // "osc133" or "regex"
	MarkerCount int    // number of OSC 133 markers found

	// Extracted content for JSON/Paths modes
	JSONBlocks    []JSONBlock
	PathBlocks    []PathBlock
	FilteredJSON  []int
	FilteredPaths []int

	// Output pane navigation (vim mode)
	OutputMode     bool      // focus in output pane
	Vim            *VimState // vim navigation state (non-nil when OutputMode is true)
	LastPaneHeight int       // set by renderer, used for page movement

	// Help overlay
	ShowHelp bool // show keybinding help modal

	// Layout geometry populated each render for mouse input mapping.
	LayoutLeftW  int // left pane width
	LayoutRightW int // right pane width
	LayoutPaneH  int // pane height (rows)

	// Mouse drag state for visual selection.
	DragActive  bool // mouse button held in right pane
	DragOriginY int  // line index at drag start
	DragOriginX int  // column at drag start
}

// NewBrowser creates a browser from parsed command blocks.
func NewBrowser(blocks []CommandBlock) *Browser {
	b := &Browser{
		Blocks:      blocks,
		MultiSelect: make(map[int]bool),
	}
	b.rebuildFiltered()
	b.extractContent()
	return b
}

func (b *Browser) extractContent() {
	// Aggregate all output for JSON/path extraction
	var allOutput strings.Builder
	for _, block := range b.Blocks {
		if block.Output != "" {
			allOutput.WriteString(block.Output)
			allOutput.WriteByte('\n')
		}
	}
	combined := allOutput.String()
	b.JSONBlocks = ExtractJSON(combined)
	b.PathBlocks = ExtractPaths(combined)
	b.rebuildFilteredJSON()
	b.rebuildFilteredPaths()
}

func (b *Browser) rebuildFiltered() {
	b.FilteredIdx = b.FilteredIdx[:0]
	for i, block := range b.Blocks {
		if b.SearchQuery == "" || containsFold(block.Command, b.SearchQuery) || containsFold(block.Output, b.SearchQuery) {
			b.FilteredIdx = append(b.FilteredIdx, i)
		}
	}
	b.clampSelection()
}

func (b *Browser) rebuildFilteredJSON() {
	b.FilteredJSON = b.FilteredJSON[:0]
	for i, jb := range b.JSONBlocks {
		if b.SearchQuery == "" || containsFold(jb.Pretty, b.SearchQuery) || containsFold(jb.Raw, b.SearchQuery) {
			b.FilteredJSON = append(b.FilteredJSON, i)
		}
	}
}

func (b *Browser) rebuildFilteredPaths() {
	b.FilteredPaths = b.FilteredPaths[:0]
	for i, pb := range b.PathBlocks {
		if b.SearchQuery == "" || containsFold(pb.Raw, b.SearchQuery) || containsFold(pb.Path, b.SearchQuery) {
			b.FilteredPaths = append(b.FilteredPaths, i)
		}
	}
}

func (b *Browser) clampSelection() {
	count := b.filteredCount()
	if count == 0 {
		b.SelectedIdx = 0
		return
	}
	if b.SelectedIdx >= count {
		b.SelectedIdx = count - 1
	}
	if b.SelectedIdx < 0 {
		b.SelectedIdx = 0
	}
}

func (b *Browser) filteredCount() int {
	switch b.Mode {
	case ModeJSON:
		return len(b.FilteredJSON)
	case ModePaths:
		return len(b.FilteredPaths)
	default:
		return len(b.FilteredIdx)
	}
}

// Next moves the cursor down.
func (b *Browser) Next() {
	if b.SelectedIdx < b.filteredCount()-1 {
		b.SelectedIdx++
	}
	b.PreviewScroll = 0
}

// Prev moves the cursor up.
func (b *Browser) Prev() {
	if b.SelectedIdx > 0 {
		b.SelectedIdx--
	}
	b.PreviewScroll = 0
}

// PageDown moves the cursor down by a page.
func (b *Browser) PageDown(pageSize int) {
	b.SelectedIdx += pageSize
	b.clampSelection()
	b.PreviewScroll = 0
}

// PageUp moves the cursor up by a page.
func (b *Browser) PageUp(pageSize int) {
	b.SelectedIdx -= pageSize
	b.clampSelection()
	b.PreviewScroll = 0
}

// ScrollPreviewDown scrolls the preview pane down.
func (b *Browser) ScrollPreviewDown() {
	b.PreviewScroll++
}

// ScrollPreviewUp scrolls the preview pane up.
func (b *Browser) ScrollPreviewUp() {
	if b.PreviewScroll > 0 {
		b.PreviewScroll--
	}
}

// ToggleSelect toggles multi-selection on the current item.
func (b *Browser) ToggleSelect() {
	if b.filteredCount() == 0 {
		return
	}
	idx := b.selectedRealIdx()
	if idx < 0 {
		return
	}
	if b.MultiSelect[idx] {
		delete(b.MultiSelect, idx)
	} else {
		b.MultiSelect[idx] = true
	}
}

// SetSearch updates the search query and rebuilds the filtered list.
func (b *Browser) SetSearch(query string) {
	b.SearchQuery = query
	b.rebuildFiltered()
	b.rebuildFilteredJSON()
	b.rebuildFilteredPaths()
}

// CycleMode cycles through browser modes.
func (b *Browser) CycleMode() {
	b.Mode = (b.Mode + 1) % 3
	b.SelectedIdx = 0
	b.PreviewScroll = 0
	b.ScrollOffset = 0
}

// SetMode switches to a specific mode.
func (b *Browser) SetMode(mode BrowserMode) {
	b.Mode = mode
	b.SelectedIdx = 0
	b.PreviewScroll = 0
	b.ScrollOffset = 0
}

// SelectedCommand returns the currently selected command block, or nil.
func (b *Browser) SelectedCommand() *CommandBlock {
	if b.Mode != ModeCommands || len(b.FilteredIdx) == 0 || b.SelectedIdx >= len(b.FilteredIdx) {
		return nil
	}
	return &b.Blocks[b.FilteredIdx[b.SelectedIdx]]
}

// SelectedJSON returns the currently selected JSON block, or nil.
func (b *Browser) SelectedJSON() *JSONBlock {
	if b.Mode != ModeJSON || len(b.FilteredJSON) == 0 || b.SelectedIdx >= len(b.FilteredJSON) {
		return nil
	}
	return &b.JSONBlocks[b.FilteredJSON[b.SelectedIdx]]
}

// SelectedPath returns the currently selected path block, or nil.
func (b *Browser) SelectedPath() *PathBlock {
	if b.Mode != ModePaths || len(b.FilteredPaths) == 0 || b.SelectedIdx >= len(b.FilteredPaths) {
		return nil
	}
	return &b.PathBlocks[b.FilteredPaths[b.SelectedIdx]]
}

// SelectedText returns the copyable text for the current selection.
func (b *Browser) SelectedText() string {
	switch b.Mode {
	case ModeCommands:
		if cmd := b.SelectedCommand(); cmd != nil {
			return cmd.Output
		}
	case ModeJSON:
		if jb := b.SelectedJSON(); jb != nil {
			return jb.Pretty
		}
	case ModePaths:
		if pb := b.SelectedPath(); pb != nil {
			return pb.Raw
		}
	}
	return ""
}

// SelectedStyledText returns the ANSI-styled output for the current selection.
func (b *Browser) SelectedStyledText() string {
	if b.Mode != ModeCommands {
		return b.SelectedText()
	}
	if cmd := b.SelectedCommand(); cmd != nil {
		if cmd.StyledOutput != "" {
			return cmd.StyledOutput
		}
		return cmd.Output
	}
	return ""
}

// SelectedCommandText returns the command string for paste-back.
func (b *Browser) SelectedCommandText() string {
	switch b.Mode {
	case ModeCommands:
		if cmd := b.SelectedCommand(); cmd != nil {
			return cmd.Command
		}
	case ModeJSON:
		if jb := b.SelectedJSON(); jb != nil {
			return jb.Raw
		}
	case ModePaths:
		if pb := b.SelectedPath(); pb != nil {
			return pb.Path
		}
	}
	return ""
}

// selectedRealIdx returns the real (unfiltered) index for the current selection.
func (b *Browser) selectedRealIdx() int {
	switch b.Mode {
	case ModeJSON:
		if b.SelectedIdx < len(b.FilteredJSON) {
			return b.FilteredJSON[b.SelectedIdx]
		}
	case ModePaths:
		if b.SelectedIdx < len(b.FilteredPaths) {
			return b.FilteredPaths[b.SelectedIdx]
		}
	default:
		if b.SelectedIdx < len(b.FilteredIdx) {
			return b.FilteredIdx[b.SelectedIdx]
		}
	}
	return -1
}

// containsFold does a case-insensitive substring match.
//
// This is the one search in the tree that deliberately does not use the scored
// matcher in pkg/fuzzy. Two of its three fields are whole command outputs, and
// a subsequence match over a multi-kilobyte blob admits almost every block, so
// "err" would filter nothing out. Scrollback also reads in time order, and
// reordering blocks by match quality would put a reply above the command that
// produced it. Grep semantics are the right ones here.
func containsFold(text, query string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

// --- Output mode methods ---

// EnterOutputMode enters the output pane with vim-style navigation.
func (b *Browser) EnterOutputMode() {
	text := b.SelectedText()
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return
	}

	// Get styled lines for colored rendering
	var styledLines []string
	if styled := b.SelectedStyledText(); styled != "" && styled != text {
		styledLines = strings.Split(styled, "\n")
	}

	b.OutputMode = true
	b.Vim = NewVimState(lines, styledLines, b.PreviewScroll)
}

// ExitOutputMode returns focus to the command list.
func (b *Browser) ExitOutputMode() {
	if b.Vim != nil {
		b.PreviewScroll = b.Vim.ScrollY
	}
	b.OutputMode = false
	b.Vim = nil
}
