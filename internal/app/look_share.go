package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/lookshare"
	"tuios.dev/shot"
)

type lookShareRequest struct {
	name, author string
	requestID    string
	grid         *shot.Grid
	pieces       []shotPiece
	settings     capture.Settings
}

type lookShareAuthorMsg struct {
	req    lookShareRequest
	author string
}

type lookShareDoneMsg struct {
	req     lookShareRequest
	comment string
	path    string
	shotErr error
	opened  bool
	tooLong bool
}

// ShareLook starts the share flow. requestID is the routed command waiting
// for the result, or empty.
func (m *OS) ShareLook(name, author, requestID string) tea.Cmd {
	if m.learnOff(learnNoteShot) {
		m.replyLookShare(requestID, errors.New("the browser tour cannot share a look"), nil)
		return nil
	}
	if m.UserConfig == nil {
		m.failLookShare(requestID, errors.New("no configuration is loaded"))
		return nil
	}
	m.settleImagesForCapture()
	grid, pieces := m.composedShot(0, 0, m.GetRenderWidth(), m.GetRenderHeight())
	settings := m.screenshotSettings()
	settings.Format = shot.FormatPNG
	settings.Title = m.Settings.FormatScreenshotTitle(settings.Title, "screen", m.FocusedWindow+1, m.focusedCWD())
	req := lookShareRequest{name: name, author: author, requestID: requestID, grid: grid, pieces: pieces, settings: settings}
	if author != "" {
		return m.handleLookShareAuthor(lookShareAuthorMsg{req: req, author: author})
	}
	return func() tea.Msg {
		return lookShareAuthorMsg{req: req, author: lookshare.Author()}
	}
}

func (m *OS) handleLookShareAuthor(msg lookShareAuthorMsg) tea.Cmd {
	req := msg.req
	ex, err := config.ExportLook(m.UserConfig, config.LookExportOptions{Name: req.name, Author: msg.author})
	if err != nil {
		m.failLookShare(req.requestID, err)
		return nil
	}
	comment := lookshare.Comment(req.name, lookshare.Summary(m.UserConfig), ex.Text)
	local := !m.RemoteClient || m.SSHIsLoopback
	opener := m.Settings.LinkOpener
	work := func() tea.Msg {
		done := lookShareDoneMsg{req: req, comment: comment, tooLong: lookshare.TooLong(ex.Text)}
		done.path, done.shotErr = saveLookShot(req)
		if local {
			done.opened = OpenWebLink(opener, lookshare.DiscussionURL) == nil
		}
		return done
	}
	return tea.Batch(m.clipboardWriteCmd(comment), work)
}

func saveLookShot(req lookShareRequest) (string, error) {
	if req.grid == nil {
		return "", errors.New("there is no screen to capture")
	}
	path, err := lookshare.ShotPath(time.Now())
	if err != nil {
		return "", err
	}
	palette, _ := capture.Palette(req.settings.ThemeID)
	frame, _ := capture.Frame(req.settings, palette, req.grid, true)
	grid := req.grid
	if images := decodeShotPieces(req.pieces); len(images) > 0 {
		withImages := *grid
		withImages.Images = images
		grid = &withImages
	}
	data, _, err := renderCaptureBytes(shot.FormatPNG, grid, frame, false)
	if err != nil {
		return "", err
	}
	if err := capture.Save(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func (m *OS) handleLookShareDone(msg lookShareDoneMsg) {
	lines := lookShareLines(msg.path, msg.shotErr == nil, msg.opened)
	if msg.tooLong {
		lines = append(lines, lookshare.MsgTooLong)
	}
	toast := lookshare.MsgToast
	if !msg.opened {
		toast = lookshare.MsgToastNoBrowser
	}
	m.ShowNotification(toast, "success", 3*m.Settings.NotificationDuration)
	m.rememberMessage(messageEntry{Text: strings.Join(lines, " "), Level: "success", Time: time.Now(), Source: "tuios"})
	if msg.shotErr != nil {
		m.LogError("share my look: the screenshot failed: %v", msg.shotErr)
	}
	m.replyLookShare(msg.req.requestID, nil, map[string]any{
		"comment":  msg.comment,
		"path":     msg.path,
		"url":      lookshare.DiscussionURL,
		"opened":   msg.opened,
		"copied":   true,
		"too_long": msg.tooLong,
	})
	m.MarkAllDirty()
}

func lookShareLines(path string, shotOK, opened bool) []string {
	lines := []string{lookshare.MsgCopied}
	if shotOK && path != "" {
		lines = append(lines, fmt.Sprintf(lookshare.MsgShot, shortenPath(path)))
	} else {
		lines = append(lines, lookshare.MsgNoShot)
	}
	if !opened {
		lines = append(lines, fmt.Sprintf(lookshare.MsgOpen, lookshare.DiscussionURL))
	}
	return lines
}

func (m *OS) failLookShare(requestID string, err error) {
	m.ShowNotification("tuios cannot share the look: "+err.Error(), "error", 2*m.Settings.NotificationDuration)
	m.replyLookShare(requestID, err, nil)
}

func (m *OS) replyLookShare(requestID string, err error, data map[string]any) {
	if m.DaemonClient == nil || requestID == "" {
		return
	}
	if err != nil {
		_ = m.DaemonClient.SendCommandResult(requestID, false, err.Error())
		return
	}
	_ = m.DaemonClient.SendCommandResultWithData(requestID, true, "command executed", data)
}
