package client

import (
	"encoding/json"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Copies between machines: this machine's daemon runs them (tuios cp), goes
// on after a dropped link and checks each file with sha256. The app starts
// them, lists them and follows them.

// Transfer is one copy, as transfer-list reports it.
type Transfer = session.TransferRow

// Endpoint is a path on a machine: Host "" is this one.
type Endpoint = session.Endpoint

// StartTransfer copies src to dst. A dst with no path is the receive folder
// of its machine. A name that is there already is kept, and the copy takes
// another name beside it.
func (c *Client) StartTransfer(src, dst Endpoint) (Transfer, error) {
	raw, err := c.verb("transfer-start", map[string]any{"src": src, "dst": dst, "conflict": "keep-both"})
	if err != nil {
		return Transfer{}, err
	}
	var res struct {
		Transfer Transfer `json:"transfer"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.Transfer, nil
}

// Transfers lists the copies the daemon holds, running and ended.
func (c *Client) Transfers() ([]Transfer, error) {
	raw, err := c.verb("transfer-list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Transfers []Transfer `json:"transfers"`
	}
	err = json.Unmarshal(raw, &res)
	return res.Transfers, err
}

// CancelTransfer stops a copy.
func (c *Client) CancelTransfer(id string) error {
	_, err := c.verb("transfer-cancel", map[string]any{"id": id})
	return err
}

// ClearTransfers forgets the copies that ended.
func (c *Client) ClearTransfers() error {
	_, err := c.verb("transfer-clear", map[string]any{})
	return err
}

// WatchTransfers calls fn after the transfer events of a burst, at most every
// 250 ms, on a connection of its own, until the client closes. With no copy
// running, no event comes and nothing runs.
func (c *Client) WatchTransfers(fn func()) {
	go func() {
		vc, err := c.dial()
		if err != nil {
			return
		}
		defer vc.Close()
		go func() {
			<-c.closed
			vc.Close()
		}()
		if _, err := vc.Call("subscribe", map[string]any{"types": []string{session.EventTransfer, session.EventTransferProgress}}); err != nil {
			return
		}
		kick := make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-kick:
				case <-c.closed:
					return
				}
				fn()
				time.Sleep(250 * time.Millisecond)
			}
		}()
		for {
			if _, err := vc.ReadEventLine(0); err != nil {
				return
			}
			select {
			case kick <- struct{}{}:
			default:
			}
		}
	}()
}
