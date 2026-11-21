//go:build !unix

package app

func (m *OS) replyPixelsReady() bool { return false }

func (m *OS) flushReplyPreviewForFrame() {}
