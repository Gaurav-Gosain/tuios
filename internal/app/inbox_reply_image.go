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

type replyImageMsg struct {
	comp *composer.Model
	img  *composer.Image
	err  error
	text string
}

func (m *OS) replyImagesOK() bool {
	return m.imagePasteReason() == ""
}

func (m *OS) inboxReplyClipboard(ed *inboxReplyEditor) tea.Cmd {
	return m.composerClipboardCmd(ed.comp, ed.window)
}

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

func (m *OS) composerLive(comp *composer.Model) bool {
	if comp == nil {
		return false
	}
	if ed := m.Inbox.reply.editor; ed != nil && ed.comp == comp {
		return true
	}
	if sp := m.Inbox.ck.spawn; sp != nil && sp.task == comp {
		return true
	}
	if tc := m.taskCapture; tc != nil && tc.ed == comp && m.TaskCaptureOpen() {
		return true
	}
	return m.AgentMail.Composing && m.AgentMail.Comp == comp
}

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

func (m *OS) inboxReplyPasteCmd(ed *inboxReplyEditor, text string) tea.Cmd {
	return m.composerPasteCmd(ed.comp, ed.window, text)
}

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

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".tif": true, ".tiff": true}

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
