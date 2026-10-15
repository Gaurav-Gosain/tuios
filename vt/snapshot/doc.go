// Package snapshot captures what an emulator holds, packs it for a wire or a
// file, and brings another emulator to the same state.
//
// Capture reads a vt.Terminal into a State: the screen and the newest
// scrollback rows as cells, and everything that decides how later output is
// painted and is not in the cells, such as the modes, the pen, the scroll
// region, the tab stops and a sequence the parser is part way through. Apply
// is the reverse. Pack and Unpack switch the cells between a plain form and a
// packed form that is a fraction of the size. Replay turns a State into the
// bytes that paint it on any xterm-compatible emulator. CaptureHistory and
// ApplyHistory keep a pane's history across a restart.
//
// A State encodes with encoding/gob and encoding/json. Its fields are only
// added to, so a peer of an older build reads a newer State and the other way
// round.
package snapshot
