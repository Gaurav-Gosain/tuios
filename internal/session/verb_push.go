package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/pushnotify"
)

const pushMaxDeviceName = 64

var pushPushableKinds = AttentionKindNames

func (d *Daemon) requirePushPerson(cs *connState, verb, nonce string) *verbError {
	if !d.humanNonceHeld(nonce, cs) {
		return hintedVerbError(ErrVerbNotHuman, verb+" is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was changed. Pass the nonce of an attach or of attach-presence, from the same process. A process inside a pane never can.",
		})
	}
	if _, ok := d.humanNonceFor(nonce, "", cs); !ok {
		return nonceScopeError(verb)
	}
	return nil
}

func validDeviceName(s string) bool {
	if s == "" || len(s) > pushMaxDeviceName || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

func (d *Daemon) pushNotifierOf() *webPusher {
	if d.notify == nil {
		return nil
	}
	return d.notify.web
}

func (d *Daemon) verbRegisterPush(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Endpoint   string   `json:"endpoint"`
		P256dh     string   `json:"p256dh"`
		Auth       string   `json:"auth"`
		Device     string   `json:"device"`
		Kinds      []string `json:"kinds"`
		HumanNonce string   `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := d.requirePushPerson(cs, "register-push", p.HumanNonce); verr != nil {
		return nil, verr
	}
	w := d.pushNotifierOf()
	if w == nil {
		return nil, newVerbError(ErrVerbInternal, "this daemon has no push sender")
	}
	if !validDeviceName(p.Device) {
		return nil, invalidParam("device", "device must be 1 to 64 bytes of printable text, with no space at either end")
	}
	cfg := d.notify.cfg.Load()
	if _, err := pushnotify.CheckEndpoint(p.Endpoint, cfg != nil && cfg.WebPushInsecure()); err != nil {
		return nil, invalidParam("endpoint", err.Error())
	}
	if _, _, err := pushnotify.ParseSubscriptionKeys(p.P256dh, p.Auth); err != nil {
		param := "p256dh"
		if strings.HasPrefix(err.Error(), "auth") {
			param = "auth"
		}
		return nil, invalidParam(param, err.Error())
	}
	kinds := slices.Clone(pushDefaultKinds)
	if p.Kinds != nil {
		if len(p.Kinds) == 0 {
			return nil, invalidParam("kinds", "kinds is empty. Leave it out for the default kinds", pushPushableKinds...)
		}
		kinds = nil
		for _, k := range p.Kinds {
			if !slices.Contains(pushPushableKinds, k) {
				return nil, invalidParam("kinds", "kinds: "+echoName(k)+" is not an Inbox kind", pushPushableKinds...)
			}
			if !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
	}
	viaLink := cs != nil && cs.viaLink
	replaced, err := w.register(pushDevice{
		Device: p.Device,
		Kinds:  kinds,
		Subscription: pushnotify.Subscription{
			Endpoint: p.Endpoint,
			P256dh:   strings.TrimRight(p.P256dh, "="),
			Auth:     strings.TrimRight(p.Auth, "="),
		},
		Created: time.Now().Unix(),
	}, !viaLink)
	switch {
	case errors.Is(err, errPushExists):
		return nil, hintedVerbError(ErrVerbForbidden, "a phone named "+echoName(p.Device)+" is registered, and a link can not replace it", &VerbHint{
			Param:  "device",
			Detail: "Nothing was registered. Use another name, or replace the phone on this machine with tuios notify push register.",
		})
	case errors.Is(err, errPushFull):
		return nil, hintedVerbError(ErrVerbInvalidParams, "16 phones are registered, which is the limit", &VerbHint{
			Param:  "device",
			Verb:   "remove-push",
			Detail: "Nothing was registered. Remove a phone with remove-push, or register again under the name of one you have.",
		})
	case err != nil:
		return nil, newVerbError(ErrVerbInternal, "cannot save the phone: "+err.Error())
	}
	key, err := w.publicKey()
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot read the VAPID key: "+err.Error())
	}
	by := "this machine"
	if viaLink {
		by = "the link from " + linkPeerLabel(cs)
	}
	LogBasic("Phone %q registered for push (%s) by %s, replaced=%v", p.Device, hostOfEndpoint(p.Endpoint), by, replaced)
	d.attention.notePhone(p.Device, phoneRegisteredNote(hostOfEndpoint(p.Endpoint), by, replaced))
	return map[string]any{
		"type":             "push_registered",
		"device":           p.Device,
		"kinds":            kinds,
		"replaced":         replaced,
		"vapid_public_key": key,
		"machine":          d.manager.HostName(),
	}, nil
}

func (d *Daemon) verbListPush(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := d.requirePushPerson(cs, "list-push", p.HumanNonce); verr != nil {
		return nil, verr
	}
	w := d.pushNotifierOf()
	if w == nil {
		return nil, newVerbError(ErrVerbInternal, "this daemon has no push sender")
	}
	key, err := w.publicKey()
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot read the VAPID key: "+err.Error())
	}
	devices := []map[string]any{}
	for _, dev := range w.list() {
		origin, _ := pushnotify.CheckEndpoint(dev.Endpoint, true)
		row := map[string]any{
			"device":  dev.Device,
			"kinds":   dev.Kinds,
			"service": origin,
			"created": dev.Created,
		}
		if dev.LastOK != 0 {
			row["last_ok"] = dev.LastOK
		}
		if dev.LastError != "" {
			row["last_error"] = dev.LastError
		}
		devices = append(devices, row)
	}
	return map[string]any{
		"type":             "push_devices",
		"devices":          devices,
		"vapid_public_key": key,
		"machine":          d.manager.HostName(),
	}, nil
}

func (d *Daemon) verbRemovePush(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Device     string `json:"device"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := d.requirePushPerson(cs, "remove-push", p.HumanNonce); verr != nil {
		return nil, verr
	}
	w := d.pushNotifierOf()
	if w == nil {
		return nil, newVerbError(ErrVerbInternal, "this daemon has no push sender")
	}
	if p.Device == "" {
		return nil, invalidParam("device", "device is required")
	}
	removed, err := w.remove(p.Device)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot save the phones: "+err.Error())
	}
	if !removed {
		var names []string
		for _, dev := range w.list() {
			names = append(names, dev.Device)
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, "no phone is registered as "+echoName(p.Device), &VerbHint{
			Param:     "device",
			Available: names,
			Verb:      "list-push",
		})
	}
	if cs != nil && cs.viaLink {
		LogBasic("Phone %q removed from push by the link from %s", p.Device, linkPeerLabel(cs))
		d.attention.notePhone(p.Device, "Removed from Web Push by the link from "+linkPeerLabel(cs)+". It gets no pushes now.")
	} else {
		LogBasic("Phone %q removed from push", p.Device)
		d.attention.closePhone(p.Device)
	}
	return map[string]any{"type": "push_removed", "device": p.Device, "removed": true}, nil
}

var errPushExists = errors.New("a phone of that name is registered")

func linkPeerLabel(cs *connState) string {
	if cs == nil || cs.linkPeer == "" {
		return "another machine"
	}
	return cs.linkPeer
}

func phoneRegisteredNote(service, by string, replaced bool) string {
	if replaced {
		return "Registered again for Web Push (" + service + ") by " + by + ", in place of the phone of that name. Did you do this? If not, remove it."
	}
	return "Registered for Web Push (" + service + ") by " + by + ". Did you do this? If not, remove it with tuios notify push rm."
}

func phoneNoticeName(device string) string { return "phone " + device }

func (a *attentionStore) notePhone(device, summary string) {
	a.noteConfigNotice(phoneNoticeName(device), summary)
}

func (a *attentionStore) closePhone(device string) {
	a.closeConfigNotice(phoneNoticeName(device))
}
