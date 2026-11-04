package proto

// What a clear of a pane clears (Stream.Clear).
const (
	// ClearScreen: the scrollback and the screen above the cursor's line,
	// which moves to the top, as ⌘K and the context menu's Clear do
	// (terminal.ClearScreen).
	ClearScreen byte = 's'
	// ClearScrollback: the scrollback (terminal.ClearScrollback), which
	// older apps' context menu asked with Clear Scrollback.
	ClearScrollback byte = 'b'
)
