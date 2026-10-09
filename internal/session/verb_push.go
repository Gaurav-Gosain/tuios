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

// register-push, list-push and remove-push: the person's phones for Web Push
// (daemon_webpush.go).
//
// A registered phone gets the Inbox's lines off the machine, so only the
// person may register one, list them or remove one: a live human_nonce,
// checked as reply-approval checks it (an attach nonce, or the nonce of
// attach-presence), and over a link the respond capability. An agent in a
// pane can do none of it.

// pushMaxDeviceName bounds a phone's name, in bytes.
const pushMaxDeviceName = 64

// pushPushableKinds are the kinds a phone may ask for: every Inbox kind.
var pushPushableKinds = AttentionKindNames

// requirePushPerson is the person check every push verb makes.
func (d *Daemon) requirePushPerson(cs *connState, verb, nonce string) *verbError {
	if d.humanNonceHeld(nonce, cs) {
		return nil
	}
	return hintedVerbError(ErrVerbNotHuman, verb+" is for the person at an attached client", &VerbHint{
		Param:  "human_nonce",
		Detail: "Nothing was changed. Pass the nonce of an attach or of attach-presence, from the same process. A process inside a pane never can.",
	})
}

// validDeviceName reports whether a phone's name is 1 to 64 bytes of
// printable text with no line break.
func validDeviceName(s string) bool {
	if s == "" || len(s) > pushMaxDeviceName || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// pushNotifierOf is the daemon's web pusher, or nil in a daemon built
// without one.
func (d *Daemon) pushNotifierOf() *webPusher {
	if d.notify == nil {
		return nil
	}
	return d.notify.web
}

// verbRegisterPush registers a phone's push subscription.
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
	replaced, err := w.register(pushDevice{
		Device: p.Device,
		Kinds:  kinds,
		Subscription: pushnotify.Subscription{
			Endpoint: p.Endpoint,
			P256dh:   strings.TrimRight(p.P256dh, "="),
			Auth:     strings.TrimRight(p.Auth, "="),
		},
		Created: time.Now().Unix(),
	})
	switch {
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
	LogBasic("Phone %q registered for push (%s)", p.Device, hostOfEndpoint(p.Endpoint))
	return map[string]any{
		"type":             "push_registered",
		"device":           p.Device,
		"kinds":            kinds,
		"replaced":         replaced,
		"vapid_public_key": key,
		"machine":          d.manager.HostName(),
	}, nil
}

// verbListPush lists the registered phones. An endpoint is shown only as its
// origin: the whole address lets anyone send to the phone.
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

// verbRemovePush removes a registered phone.
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
	LogBasic("Phone %q removed from push", p.Device)
	return map[string]any{"type": "push_removed", "device": p.Device, "removed": true}, nil
}
