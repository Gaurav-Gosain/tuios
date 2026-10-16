//go:build !unix

package app

// replyPixelsReady is false: the kitty escapes are built in the unix files
// only, as the launcher's icons are. The previews are glyphs or words.
func (m *OS) replyPixelsReady() bool { return false }

// flushReplyPreviewForFrame has nothing to put on the host.
func (m *OS) flushReplyPreviewForFrame() {}
