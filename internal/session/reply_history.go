package session

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The person's reply history: what they sent to each agent, so the reply
// editor can bring an earlier message back with up and ctrl+r.
//
// It is kept in the daemon's memory and nowhere else, and it ends with the
// daemon. Replies hold pasted logs and secrets, so nothing of them is written
// to disk. It is the person's alone: both verbs take a live human_nonce, by
// the rules every person's verb follows (human_sender.go), so no pane and no
// caller without the nonce reads or adds to it. Over a link that also needs
// respond, as agent-transcript does.
//
// A target is the key the client gives, such as the harness and the folder of
// the pane, so a pane that closes and opens again on the same folder keeps its
// history. Each target keeps replyHistoryEntries messages, at most
// replyHistoryTargets targets are kept, the least used going first, one
// message keeps at most replyHistoryEntryBytes of pastes and images (past
// that the largest are dropped, and the entry names what was dropped), and
// the whole history holds at most replyHistoryBytes.

const (
	replyHistoryEntries    = 100
	replyHistoryTargets    = 64
	replyHistoryEntryBytes = 256 << 10
	replyHistoryTargetMax  = 512
	// replyHistoryBytes bounds the whole history. Past it the oldest
	// messages of the least used targets go first.
	replyHistoryBytes = 32 << 20
)

// replyHistoryEntry is one sent message.
type replyHistoryEntry struct {
	Parts []queuePart `json:"parts"`
	At    int64       `json:"at"`
	// Dropped names the blocks that were too large to keep, such as
	// "Pasted text #1".
	Dropped []string `json:"dropped,omitempty"`
}

// replyHistory is every target's history. The zero value is ready.
type replyHistory struct {
	mu      sync.Mutex
	targets map[string][]replyHistoryEntry
	order   []string
	// bytes is what every entry holds, for replyHistoryBytes.
	bytes int
}

// entryBytes is what an entry holds.
func entryBytes(e replyHistoryEntry) int {
	n := 0
	for _, p := range e.Parts {
		n += len(p.Text) + len(p.Data)
	}
	return n
}

// add keeps a message for target, newest last.
func (h *replyHistory) add(target string, e replyHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.targets == nil {
		h.targets = make(map[string][]replyHistoryEntry)
	}
	list := h.targets[target]
	// The same message again moves to the end, so up brings it back first.
	list = slices.DeleteFunc(list, func(o replyHistoryEntry) bool {
		if sameParts(o.Parts, e.Parts) {
			h.bytes -= entryBytes(o)
			return true
		}
		return false
	})
	list = append(list, e)
	h.bytes += entryBytes(e)
	for len(list) > replyHistoryEntries {
		h.bytes -= entryBytes(list[0])
		list = slices.Delete(list, 0, 1)
	}
	h.targets[target] = list
	h.touchLocked(target)
	// Past the whole bound, the oldest messages of the least used targets
	// go, never the one just added.
	for i := 0; h.bytes > replyHistoryBytes && i < len(h.order); {
		t := h.order[i]
		l := h.targets[t]
		if t == target && len(l) <= 1 {
			i++
			continue
		}
		if len(l) == 0 {
			i++
			continue
		}
		h.bytes -= entryBytes(l[0])
		h.targets[t] = slices.Delete(l, 0, 1)
	}
}

// touchLocked marks target the most recently used and drops the least used
// past the bound.
func (h *replyHistory) touchLocked(target string) {
	h.order = slices.DeleteFunc(h.order, func(o string) bool { return o == target })
	h.order = append(h.order, target)
	for len(h.order) > replyHistoryTargets {
		for _, e := range h.targets[h.order[0]] {
			h.bytes -= entryBytes(e)
		}
		delete(h.targets, h.order[0])
		h.order = h.order[1:]
	}
}

// list is target's history, oldest first. The slice is a copy.
func (h *replyHistory) list(target string) []replyHistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	list, ok := h.targets[target]
	if ok {
		h.touchLocked(target)
	}
	return slices.Clone(list)
}

// sameParts reports whether two messages are the same.
func sameParts(a, b []queuePart) bool {
	return slices.EqualFunc(a, b, func(x, y queuePart) bool {
		return x.Kind == y.Kind && x.Text == y.Text && x.N == y.N && string(x.Data) == string(y.Data)
	})
}

// boundHistoryParts drops the largest pastes and images until the message's
// blocks fit replyHistoryEntryBytes, and names what it dropped.
func boundHistoryParts(parts []queuePart) ([]queuePart, []string) {
	size := 0
	for _, p := range parts {
		if p.Kind != queuePartText {
			size += len(p.Text) + len(p.Data)
		}
	}
	var dropped []string
	for size > replyHistoryEntryBytes {
		big := -1
		for i, p := range parts {
			if p.Kind != queuePartText && (big < 0 || len(p.Text)+len(p.Data) > len(parts[big].Text)+len(parts[big].Data)) {
				big = i
			}
		}
		if big < 0 {
			break
		}
		p := parts[big]
		size -= len(p.Text) + len(p.Data)
		name := "Pasted text #" + strconv.Itoa(p.N)
		if p.Kind == queuePartImage {
			name = "Image #" + strconv.Itoa(p.N)
		}
		dropped = append(dropped, name)
		parts = slices.Delete(slices.Clone(parts), big, big+1)
	}
	return parts, dropped
}

// replyHistoryRefusal is the refusal for a caller that is not the person.
func replyHistoryRefusal(verb string) *verbError {
	return hintedVerbError(ErrVerbNotHuman, verb+" is for the person at an attached client", &VerbHint{
		Param:  "human_nonce",
		Detail: "The reply history holds what the person sent, pasted logs included. Only a client attached right now, with the nonce its attach reply carried, reads or adds to it.",
	})
}

// replyHistoryHuman reports whether the caller is the person for the reply
// history. A target is the client's own key and belongs to no one session,
// so it takes a nonce that proves the person for an act in no one session:
// an attach nonce, or a presence made with no session. A presence made for
// one session reads nothing, since the history holds what was sent to the
// agents of every session.
func (d *Daemon) replyHistoryHuman(cs *connState, nonce string) bool {
	if cs != nil && cs.paneOnly {
		return false
	}
	_, ok := d.humanNonceFor(nonce, "", cs)
	return ok
}

// replyHistoryTarget checks a target key.
func replyHistoryTarget(target string) *verbError {
	if strings.TrimSpace(target) == "" {
		return invalidParam("target", "target is required: the key the client keeps the history under")
	}
	if len(target) > replyHistoryTargetMax {
		return invalidParam("target", "target is longer than "+strconv.Itoa(replyHistoryTargetMax)+" bytes")
	}
	return nil
}

// verbAddReplyHistory keeps a message the person sent.
func (d *Daemon) verbAddReplyHistory(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Target     string      `json:"target"`
		Parts      []queuePart `json:"parts"`
		HumanNonce string      `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !d.replyHistoryHuman(cs, p.HumanNonce) {
		return nil, replyHistoryRefusal("add-reply-history")
	}
	if verr := replyHistoryTarget(p.Target); verr != nil {
		return nil, verr
	}
	if _, _, verr := checkParts(p.Parts, queueMaxText); verr != nil {
		return nil, verr
	}
	parts, dropped := boundHistoryParts(p.Parts)
	d.replyHistory.add(p.Target, replyHistoryEntry{Parts: parts, At: time.Now().UnixMilli(), Dropped: dropped})
	return map[string]any{"type": "reply_history_added", "target": p.Target, "dropped": dropped}, nil
}

// verbReplyHistory reads a target's history.
func (d *Daemon) verbReplyHistory(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Target     string `json:"target"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !d.replyHistoryHuman(cs, p.HumanNonce) {
		return nil, replyHistoryRefusal("reply-history")
	}
	if verr := replyHistoryTarget(p.Target); verr != nil {
		return nil, verr
	}
	entries := d.replyHistory.list(p.Target)
	if entries == nil {
		entries = []replyHistoryEntry{}
	}
	return map[string]any{"type": "reply_history", "target": p.Target, "entries": entries}, nil
}
