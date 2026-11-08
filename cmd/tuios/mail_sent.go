package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// sentReceipts is the agent-message-receipts answer as the CLI reads it.
type sentReceipts struct {
	Receipts []struct {
		ID            uint64 `json:"id"`
		Host          string `json:"host"`
		Session       string `json:"session"`
		To            string `json:"to"`
		ToLabel       string `json:"to_label"`
		ThreadID      uint64 `json:"thread_id"`
		Preview       string `json:"preview"`
		SentAt        int64  `json:"sent_at"`
		SeenAt        int64  `json:"seen_at"`
		ReadAt        int64  `json:"read_at"`
		Undeliverable bool   `json:"undeliverable"`
	} `json:"receipts"`
	Waiting []struct {
		QueueID  uint64 `json:"queue_id"`
		Host     string `json:"host"`
		Session  string `json:"session"`
		To       string `json:"to"`
		QueuedAt int64  `json:"queued_at"`
	} `json:"waiting"`
	Unreachable []struct {
		Host   string `json:"host"`
		Reason string `json:"reason"`
	} `json:"unreachable"`
}

// runReadSentMessages is read-agent-messages --sent: the mail window sent and
// whether each was seen or read.
func runReadSentMessages(window string, limit int, jsonOutput bool) error {
	if window == "" {
		return errors.New("--sent needs the sending window: run it in a pane, or pass -w with the window id")
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"window": window}
	if limit > 0 {
		params["limit"] = limit
	}
	raw, err := client.CallWithTimeout("agent-message-receipts", params, 15*time.Second)
	if err != nil {
		return reportVerbError(explainVerbError("agent-message-receipts", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res sentReceipts
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	printSentReceipts(os.Stdout, res)
	return nil
}

// printSentReceipts writes one line per message, and a line for each machine
// that could not be asked.
func printSentReceipts(w io.Writer, res sentReceipts) {
	if len(res.Receipts) == 0 && len(res.Waiting) == 0 && len(res.Unreachable) == 0 {
		fmt.Fprintln(w, "No mail from this window is held now.")
	}
	for _, r := range res.Receipts {
		where := plainLine(r.Session)
		if r.Host != "" {
			where = plainLine(r.Host) + ":" + where
		}
		to := plainLine(r.ToLabel)
		if to == "" || to == r.To {
			to = shortWindowID(r.To)
		} else {
			to += " (" + shortWindowID(r.To) + ")"
		}
		var state string
		switch {
		case r.Undeliverable:
			state = "undeliverable: the recipient window is gone"
		case r.ReadAt != 0:
			state = "read " + agoOf(r.ReadAt)
		case r.SeenAt != 0:
			state = "seen " + agoOf(r.SeenAt) + ", not read yet"
		default:
			state = "delivered, unread"
		}
		fmt.Fprintf(w, "#%d  to %s in %s  sent %s  %s\n", r.ID, to, where, agoOf(r.SentAt), state)
		if p := plainLine(r.Preview); p != "" {
			fmt.Fprintf(w, "    %s\n", p)
		}
	}
	for _, q := range res.Waiting {
		fmt.Fprintf(w, "queued  to %s in %s:%s  waiting for the link since %s\n",
			shortWindowID(q.To), plainLine(q.Host), plainLine(q.Session), agoOf(q.QueuedAt))
	}
	if len(res.Unreachable) > 0 {
		names := make([]string, 0, len(res.Unreachable))
		for _, u := range res.Unreachable {
			names = append(names, plainLine(u.Host)+" ("+plainLine(u.Reason)+")")
		}
		fmt.Fprintf(w, "Not reachable now: %s. Mail sent there is not listed.\n", strings.Join(names, ", "))
	}
}
