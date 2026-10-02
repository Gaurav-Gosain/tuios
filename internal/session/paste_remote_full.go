//go:build !slim

package session

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// verbPastePaneImage writes an image for a pane this machine runs for another
// machine. Only that machine's daemon holds the token it needs.
func (d *Daemon) verbPastePaneImage(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane    string `json:"pane"`
		Token   string `json:"token"`
		Content string `json:"content"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "paste-pane-image needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	if hp.callsToken == "" || subtle.ConstantTimeCompare([]byte(hp.callsToken), []byte(p.Token)) != 1 {
		return nil, newVerbError(ErrVerbForbidden, "paste-pane-image: the token is not this pane's. Only the daemon that owns the window can paste into it.")
	}
	data, verr := decodePasteContent("paste-pane-image", p.Content)
	if verr != nil {
		return nil, verr
	}
	path, err := d.pastes.save(data)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "paste-pane-image: cannot write the image: "+err.Error())
	}
	return map[string]any{"pane": p.Pane, "path": path, "bytes": len(data)}, nil
}

// pasteImage sends an image to the far machine and returns the path it was
// written to there. The bytes go on a connection of their own: the link's
// control stream carries every other call, and eight megabytes on it would
// hold them all up.
func (p *remotePane) pasteImage(ctx context.Context, data []byte) (string, error) {
	if p.fed == nil {
		return "", errors.New("this daemon has no links")
	}
	if p.callsToken == "" {
		return "", errors.New("the tuios there is too old to take a pasted image. Update it, then restart its daemon")
	}
	stream, err := p.fed.OpenConnection(ctx, p.host)
	if err != nil {
		return "", err
	}
	// Closing the stream is what ends a read that ctx has given up on.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = stream.Close()
	}()

	params, err := json.Marshal(map[string]string{
		"pane":    p.id,
		"token":   p.callsToken,
		"content": base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return "", err
	}
	req, err := json.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: "paste-pane-image", Params: params})
	if err != nil {
		return "", err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return "", fmt.Errorf("cannot send the image: %w", err)
	}
	line, err := readLimitedLine(bufio.NewReader(stream), maxPasteReply)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, io.EOF) {
			return "", errors.New("the far machine closed the connection")
		}
		return "", err
	}
	var resp struct {
		Result *struct {
			Path string `json:"path"`
		} `json:"result"`
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("the reply cannot be read by this build: %w", err)
	}
	if resp.Error != nil {
		if resp.Error.Code == ErrVerbUnknownVerb {
			return "", errors.New("the tuios there is too old to take a pasted image. Update it, then restart its daemon")
		}
		return "", resp.Error
	}
	if resp.Result == nil || resp.Result.Path == "" {
		return "", errors.New("the reply named no path")
	}
	return resp.Result.Path, nil
}
