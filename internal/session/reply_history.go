package session

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	replyHistoryEntries    = 100
	replyHistoryTargets    = 64
	replyHistoryEntryBytes = 256 << 10
	replyHistoryTargetMax  = 512
	replyHistoryBytes      = 32 << 20
)

type replyHistoryEntry struct {
	Parts   []queuePart `json:"parts"`
	At      int64       `json:"at"`
	Dropped []string    `json:"dropped,omitempty"`
}

type replyHistory struct {
	mu      sync.Mutex
	targets map[string][]replyHistoryEntry
	order   []string
	bytes   int
}

func entryBytes(e replyHistoryEntry) int {
	n := 0
	for _, p := range e.Parts {
		n += len(p.Text) + len(p.Data)
	}
	return n
}

func (h *replyHistory) add(target string, e replyHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.targets == nil {
		h.targets = make(map[string][]replyHistoryEntry)
	}
	list := h.targets[target]
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

func (h *replyHistory) list(target string) []replyHistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	list, ok := h.targets[target]
	if ok {
		h.touchLocked(target)
	}
	return slices.Clone(list)
}

func sameParts(a, b []queuePart) bool {
	return slices.EqualFunc(a, b, func(x, y queuePart) bool {
		return x.Kind == y.Kind && x.Text == y.Text && x.N == y.N && string(x.Data) == string(y.Data)
	})
}

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

func replyHistoryRefusal(verb string) *verbError {
	return hintedVerbError(ErrVerbNotHuman, verb+" is for the person at an attached client", &VerbHint{
		Param:  "human_nonce",
		Detail: "The reply history holds what the person sent, pasted logs included. Only a client attached right now, with the nonce its attach reply carried, reads or adds to it.",
	})
}

func (d *Daemon) replyHistoryHuman(cs *connState, nonce string) bool {
	if cs != nil && cs.paneOnly {
		return false
	}
	_, ok := d.humanNonceFor(nonce, "", cs)
	return ok
}

func replyHistoryTarget(target string) *verbError {
	if strings.TrimSpace(target) == "" {
		return invalidParam("target", "target is required: the key the client keeps the history under")
	}
	if len(target) > replyHistoryTargetMax {
		return invalidParam("target", "target is longer than "+strconv.Itoa(replyHistoryTargetMax)+" bytes")
	}
	return nil
}

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
