package app

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// Attaching an image to a reply.
//
// The image is read on this machine, the one the person is at, with the
// clipboard tools the pane image paste uses (clipboard_image.go). Three things
// attach one, and each puts it in the draft as a block, [Image #1 · 1280×720
// png], at the cursor:
//
//   - ctrl+v in the reply. When the clipboard holds an image and no text, the
//     image is attached. When it holds text, the text is pasted as before.
//   - An empty bracketed paste, which some terminals send when the clipboard
//     holds only an image.
//   - A paste that is only the path of an image file on this machine, which is
//     what a terminal sends for a file dropped on it.
//
// The bytes stay in the draft, in memory, until the reply is sent. The daemon
// on the pane's machine writes them to a file when it types the reply.
//
// A read is slow, and the person may close the draft or start another one
// meanwhile. The result names the draft it was read for, and is dropped unless
// that draft is still the open one, so a slow clipboard never lands in a newer
// draft.

// replyImageMsg is an image read for a reply's draft, or the text of a paste
// that turned out not to be one.
type replyImageMsg struct {
	comp *composer.Model
	img  *composer.Image
	err  error
	// text is a paste to put in the draft as text: a path that named no
	// image this machine could read.
	text string
}

// replyImagesOK reports whether this client can read an image for a reply.
func (m *OS) replyImagesOK() bool {
	return m.imagePasteReason() == ""
}

// inboxReplyClipboard is ctrl+v in the reply: the clipboard's image when it
// holds one and no text, its text otherwise.
func (m *OS) inboxReplyClipboard(ed *inboxReplyEditor) tea.Cmd {
	return m.composerClipboardCmd(ed.comp, ed.window)
}

// composerClipboardCmd is ctrl+v in a draft: the clipboard's image when it
// holds one and no text, its text otherwise.
func (m *OS) composerClipboardCmd(comp *composer.Model, window string) tea.Cmd {
	if !comp.TakesImages() || !m.replyImagesOK() || m.imagePaste.keyProbeOff {
		return m.RequestHostPaste()
	}
	clip := m.imageClipboardHere()
	if clip == nil {
		return m.RequestHostPaste()
	}
	return forReply(comp, probeImageCmd(clip, window, imagePasteKey))
}

// composerLive reports whether comp is a draft open now: the Inbox reply's or
// the mail editor's. A read for any other draft is dropped.
func (m *OS) composerLive(comp *composer.Model) bool {
	if comp == nil {
		return false
	}
	if ed := m.Inbox.reply.editor; ed != nil && ed.comp == comp {
		return true
	}
	return m.AgentMail.Composing && m.AgentMail.Comp == comp
}

// forReply marks the clipboard probe's answer as the draft's.
func forReply(comp *composer.Model, probe tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg := probe()
		if p, ok := msg.(imageProbeMsg); ok {
			p.reply = comp
			return p
		}
		return msg
	}
}

// inboxReplyPasteCmd is the work a paste into the reply needs off the update
// loop: an empty paste looks for a clipboard image, and a path to an image
// file reads the file. Any other paste is text, put in the draft at once.
func (m *OS) inboxReplyPasteCmd(ed *inboxReplyEditor, text string) tea.Cmd {
	return m.composerPasteCmd(ed.comp, ed.window, text)
}

// composerPasteCmd is inboxReplyPasteCmd for any draft.
func (m *OS) composerPasteCmd(comp *composer.Model, window, text string) tea.Cmd {
	if !comp.TakesImages() || !m.replyImagesOK() {
		comp.Paste(text, m.ProcessingRemoteKeys)
		return nil
	}
	if text == "" {
		if m.imagePaste.keyProbeOff {
			return nil
		}
		clip := m.imageClipboardHere()
		if clip == nil {
			return nil
		}
		return forReply(comp, probeImageCmd(clip, window, imagePasteEmpty))
	}
	path, ok := imagePathOfPaste(text)
	if !ok {
		comp.Paste(text, m.ProcessingRemoteKeys)
		return nil
	}
	return func() tea.Msg {
		data, err := readImageFile(path)
		if errors.Is(err, composer.ErrImageTooLarge) {
			return replyImageMsg{comp: comp, err: err}
		}
		if err != nil {
			return replyImageMsg{comp: comp, text: text}
		}
		img, err := composer.NewImage(data)
		if err != nil {
			return replyImageMsg{comp: comp, text: text}
		}
		img.Name = filepath.Base(path)
		return replyImageMsg{comp: comp, img: img}
	}
}

// applyReplyImageProbe acts on what the clipboard held for a reply.
func (m *OS) applyReplyImageProbe(msg imageProbeMsg) tea.Cmd {
	if msg.keyProbeOff {
		m.imagePaste.keyProbeOff = true
	}
	if !m.composerLive(msg.reply) {
		return nil
	}
	if msg.fallback {
		return m.RequestHostPaste()
	}
	if msg.data == nil && msg.err == nil {
		return nil
	}
	if msg.err != nil {
		m.applyReplyImage(replyImageMsg{comp: msg.reply, err: msg.err})
		return nil
	}
	img, err := composer.NewImage(msg.data)
	m.applyReplyImage(replyImageMsg{comp: msg.reply, img: img, err: err})
	return m.replyPreviewCmd()
}

// applyReplyImage puts a read image in the draft it was read for.
func (m *OS) applyReplyImage(msg replyImageMsg) {
	if !m.composerLive(msg.comp) {
		return
	}
	comp := msg.comp
	switch {
	case msg.text != "":
		comp.Paste(msg.text, false)
	case errors.Is(msg.err, composer.ErrImageTooLarge), errors.Is(msg.err, errClipboardTooLarge):
		comp.SetNotice("The image is larger than 8 MB. Make it smaller and paste it again.")
	case errors.Is(msg.err, errNoClipboardImage):
		comp.SetNotice("The clipboard holds no image.")
	case msg.err != nil:
		comp.SetNotice("tuios cannot read the clipboard image: " + msg.err.Error())
	case msg.img != nil:
		comp.AddImage(msg.img, false)
	}
}

// imageExts are the file names a pasted path is read as an image for.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".tif": true, ".tiff": true}

// imagePathOfPaste is the image file a paste names, when the paste is one
// path and nothing else: a file:// URL, a quoted path, or one with its
// spaces escaped, as terminals paste a dropped file. It does not look at the
// file.
func imagePathOfPaste(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.ContainsAny(s, "\n\r") || len(s) > 4096 {
		return "", false
	}
	if rest, ok := strings.CutPrefix(s, "file://"); ok {
		u, err := url.Parse("file://" + rest)
		if err != nil {
			return "", false
		}
		s = u.Path
	} else if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'' || s[0] == '"' && s[len(s)-1] == '"') {
		s = s[1 : len(s)-1]
	} else if strings.Contains(s, `\`) && filepath.Separator == '/' {
		s = unescapeShell(s)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(s, "~/"); ok {
			s = filepath.Join(home, rest)
		}
	}
	if !filepath.IsAbs(s) || !imageExts[strings.ToLower(filepath.Ext(s))] {
		return "", false
	}
	return filepath.Clean(s), true
}

// unescapeShell takes out the backslashes a terminal puts before spaces and
// other characters of a dropped file's path.
func unescapeShell(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readImageFile reads a regular file of at most MaxImageBytes.
//
// The path comes from a paste, so it can name anything: a named pipe called
// shot.png blocks an open for reading until a writer comes, and the read would
// hold its background task for good. The path is checked before it is opened,
// anything that is not a regular file is refused, and the open does not block
// where the platform has O_NONBLOCK. The open file is checked again, so a
// pipe swapped in between the two is refused too.
func readImageFile(path string) ([]byte, error) {
	errNotFile := errors.New("not a regular file")
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errNotFile
	}
	if before.Size() > composer.MaxImageBytes {
		return nil, composer.ErrImageTooLarge
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openNonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errNotFile
	}
	if info.Size() > composer.MaxImageBytes {
		return nil, composer.ErrImageTooLarge
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil && info.Size() > 0 {
		return nil, err
	}
	return data, nil
}
