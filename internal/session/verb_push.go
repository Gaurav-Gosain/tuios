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
// person may register one, list them or remove one: a live human_nonce, and
// over a link the respond capability. An agent in a pane can do none of it.
//
// Nonce scope (human_sender.go): a phone gets the Inbox of every session, so
// a phone is an act in no one session. The nonce of an attach, or of a
// presence made with no session, covers it. A presence made for one session
// covers only what that session could see, and that is not a phone, so it
// can not register, list or remove one.
//
// Every registration opens an Inbox item that names the phone, and a new
// registration in place of one with the same name says so, so a phone the
// person did not add does not stay hidden. Over a link, a registration may
// not take the place of a phone already registered: a linked machine can add
// a phone the person then sees, but it can not swap the person's phone for
// its own.

// pushMaxDeviceName bounds a phone's name, in bytes.
const pushMaxDeviceName = 64

// pushPushableKinds are the kinds a phone may ask for: every Inbox kind.
var pushPushableKinds = AttentionKindNames

// requirePushPerson is the person check every push verb makes: a live nonce
// that covers an act in no one session.
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
	// The person's own remove closes the phone's item. A remove over a
	// link opens one: a linked machine stopped the person's pushes.
	if cs != nil && cs.viaLink {
		LogBasic("Phone %q removed from push by the link from %s", p.Device, linkPeerLabel(cs))
		d.attention.notePhone(p.Device, "Removed from Web Push by the link from "+linkPeerLabel(cs)+". It gets no pushes now.")
	} else {
		LogBasic("Phone %q removed from push", p.Device)
		d.attention.closePhone(p.Device)
	}
	return map[string]any{"type": "push_removed", "device": p.Device, "removed": true}, nil
}

// errPushExists is a register-push over a link with the name of a phone
// already registered.
var errPushExists = errors.New("a phone of that name is registered")

// linkPeerLabel names the machine at the other end of a link connection.
func linkPeerLabel(cs *connState) string {
	if cs == nil || cs.linkPeer == "" {
		return "another machine"
	}
	return cs.linkPeer
}

// phoneRegisteredNote is the summary of the Inbox item a registration opens.
// The item's name names the phone.
func phoneRegisteredNote(service, by string, replaced bool) string {
	if replaced {
		return "Registered again for Web Push (" + service + ") by " + by + ", in place of the phone of that name. Did you do this? If not, remove it."
	}
	return "Registered for Web Push (" + service + ") by " + by + ". Did you do this? If not, remove it with tuios notify push rm."
}

// phoneNoticeName is the Name of the Inbox item about a phone.
func phoneNoticeName(device string) string { return "phone " + device }

// notePhone opens or updates the Inbox item about a registered phone: an
// errored item about the daemon itself, as noteConfigNotice opens one, which
// the person dismisses.
func (a *attentionStore) notePhone(device, summary string) {
	a.noteConfigNotice(phoneNoticeName(device), summary)
}

// closePhone closes the Inbox item about a phone.
func (a *attentionStore) closePhone(device string) {
	a.closeConfigNotice(phoneNoticeName(device))
}
