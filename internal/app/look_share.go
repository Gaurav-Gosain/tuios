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

// Share my look.
//
// One action takes the person from "I like my setup" to a comment ready to
// paste in the setups discussion: the look file of the config in force, a
// screenshot of the whole screen, the comment on the clipboard, and the
// discussion open in the browser. `tuios look share` runs the same flow here,
// through the routed ShareLook command, because only the client has the
// composed screen and a terminal to carry the clipboard write over ssh.
//
// The flow takes three turns of the loop. The screen is read now, before any
// toast is on it. gh is asked for the login off this goroutine. The look is
// exported back on this goroutine, which owns the config and the theme
// registry. The picture, the native clipboard and the browser run off it again.

// lookShareRequest is one share in flight.
type lookShareRequest struct {
	name, author string
	// requestID is the CLI waiting for the result, or empty.
	requestID string
	grid      *shot.Grid
	pieces    []shotPiece
	settings  capture.Settings
}

// lookShareAuthorMsg brings the gh login back to the loop.
type lookShareAuthorMsg struct {
	req    lookShareRequest
	author string
}

// lookShareDoneMsg is a finished share.
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
	if settings.Title != "" {
		settings.Title = m.Settings.FormatWindowTitle("screen", m.FocusedWindow+1, "")
	}
	req := lookShareRequest{name: name, author: author, requestID: requestID, grid: grid, pieces: pieces, settings: settings}
	if author != "" {
		return m.handleLookShareAuthor(lookShareAuthorMsg{req: req, author: author})
	}
	return func() tea.Msg {
		return lookShareAuthorMsg{req: req, author: lookshare.Author()}
	}
}

// handleLookShareAuthor exports the look and starts the slow part.
func (m *OS) handleLookShareAuthor(msg lookShareAuthorMsg) tea.Cmd {
	req := msg.req
	ex, err := config.ExportLook(m.UserConfig, config.LookExportOptions{Name: req.name, Author: msg.author})
	if err != nil {
		m.failLookShare(req.requestID, err)
		return nil
	}
	comment := lookshare.Comment(req.name, lookshare.Summary(m.UserConfig), ex.Text)
	// A served client's person is on another machine, so a browser started
	// here opens in front of nobody.
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

// saveLookShot renders the screen the share read as a PNG.
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

// handleLookShareDone tells the person what to do next, and answers the CLI.
func (m *OS) handleLookShareDone(msg lookShareDoneMsg) {
	lines := lookShareLines(msg.path, msg.shotErr == nil, msg.opened)
	if msg.tooLong {
		lines = append(lines, lookshare.MsgTooLong)
	}
	// The dock gets a short line that is never cut. The full message, with
	// the screenshot path, is what prefix+N opens.
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

// lookShareLines are the lines the toast shows and the CLI prints.
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

// failLookShare says the share failed, and answers the CLI.
func (m *OS) failLookShare(requestID string, err error) {
	m.ShowNotification("tuios cannot share the look: "+err.Error(), "error", 2*m.Settings.NotificationDuration)
	m.replyLookShare(requestID, err, nil)
}

// replyLookShare answers a routed ShareLook command.
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
