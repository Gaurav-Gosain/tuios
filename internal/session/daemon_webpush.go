package session

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pushnotify"
)

// Web Push to the person's phones: the daemon's half.
//
// A phone registers a push subscription with register-push, which only the
// person can call (verb_push.go). From then on, when an Inbox item of a kind
// the phone asked for opens, the daemon encrypts a small JSON payload for the
// phone and posts it to the phone's push service, and when the item closes it
// posts a close, so the phone can remove the notification. The phone holds no
// connection to tuios for this: its push service, or its UnifiedPush
// distributor, wakes it.
//
// Nothing here blocks the daemon. The notifier hands each push to a queue per
// phone, and one goroutine per phone sends them in order, with retries. A
// phone whose push service answers 404 or 410 is dropped. The [notify] quiet,
// cooldown and trigger settings are for the other providers. A phone chose
// its kinds when it registered, and a close takes back what an open said, so
// the only limit here is max_per_hour, counted per phone, on opens.

// pushDefaultKinds are the kinds a phone gets when it names none: the items
// that wait for the person.
var pushDefaultKinds = []string{AttentionApproval, AttentionPlan, AttentionAsk, AttentionQuestion}

// pushMaxDevices bounds the registered phones.
const pushMaxDevices = 16

// pushDeviceQueue bounds the pushes waiting for one phone. A burst past it
// drops the oldest open first; the newest state is what matters.
const pushDeviceQueue = 64

// pushRetryDelays are the waits between attempts. Every wait together stays
// well inside the TTL, since a push the phone gets later than that is stale.
var pushRetryDelays = []time.Duration{time.Second, 4 * time.Second, 15 * time.Second}

// pushDevice is one registered phone, as it is kept on disk.
type pushDevice struct {
	Device string   `json:"device"`
	Kinds  []string `json:"kinds"`
	pushnotify.Subscription
	Created int64 `json:"created"`
	// LastOK is when a push last reached the push service, unix seconds.
	LastOK int64 `json:"last_ok,omitempty"`
	// LastError is the last failure, with no address beyond the host.
	LastError string `json:"last_error,omitempty"`
}

// wants reports whether the phone asked for this kind.
func (p *pushDevice) wants(kind string) bool { return slices.Contains(p.Kinds, kind) }

// pushPayload is the JSON a phone decrypts.
type pushPayload struct {
	V         int      `json:"v"`
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Session   string   `json:"session,omitempty"`
	Window    string   `json:"window,omitempty"`
	Host      string   `json:"host,omitempty"`
	Harness   string   `json:"harness,omitempty"`
	Name      string   `json:"name,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Risk      []string `json:"risk,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
	Options   []string `json:"options,omitempty"`
	Machine   string   `json:"machine"`
}

// Payload types.
const (
	pushOpen  = "open"
	pushClose = "close"
)

// pushJob is one push for one phone.
type pushJob struct {
	payload []byte
	urgency string
	topic   string
	itemID  string
	typ     string
}

// webPusher sends the Inbox to the registered phones.
type webPusher struct {
	n *pushNotifier

	mu      sync.Mutex
	devices map[string]*pushDevice
	loaded  bool
	key     *pushnotify.VAPIDKey
	// sent is what each open item last went out as, by id, so an update
	// that changes nothing a phone shows is not sent again.
	sent map[string]string
	// hour are each phone's open sends in the last hour.
	hour    map[string][]time.Time
	workers map[string]*pushWorker
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// pushWorker is one phone's queue and the goroutine that empties it.
type pushWorker struct {
	ch chan pushJob
}

func newWebPusher(n *pushNotifier) *webPusher {
	ctx, cancel := context.WithCancel(context.Background())
	return &webPusher{
		n:       n,
		devices: make(map[string]*pushDevice),
		sent:    make(map[string]string),
		hour:    make(map[string][]time.Time),
		workers: make(map[string]*pushWorker),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// pushDir is where the phones and the VAPID key are kept.
func pushDir() string { return filepath.Join(getResurrectionDir(), "push") }

func pushDevicesPath() string { return filepath.Join(pushDir(), "subscriptions.json") }
func pushKeyPath() string     { return filepath.Join(pushDir(), "vapid.pem") }

// writePrivate writes data to path with mode 0600, in a directory with mode
// 0700, through a temporary file.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	return os.Rename(tmp, path)
}

// loadLocked reads the phones from disk once. The caller holds mu.
func (w *webPusher) loadLocked() {
	if w.loaded {
		return
	}
	w.loaded = true
	data, err := os.ReadFile(pushDevicesPath())
	if err != nil {
		return
	}
	var list []*pushDevice
	if err := json.Unmarshal(data, &list); err != nil {
		// The next register would write over it with only the new phone.
		// Keep it beside, so the phones in it can be got back by hand.
		bad := pushDevicesPath() + ".bad"
		if rerr := os.Rename(pushDevicesPath(), bad); rerr != nil {
			log.Printf("[NOTIFY] cannot read the registered phones (%v), and cannot move the file aside: %v", err, rerr)
			return
		}
		log.Printf("[NOTIFY] cannot read the registered phones: %v. The file is now %s. Register the phones again", err, bad)
		return
	}
	for _, d := range list {
		if d != nil && d.Device != "" {
			w.devices[d.Device] = d
		}
	}
}

// saveLocked writes the phones to disk. The caller holds mu.
func (w *webPusher) saveLocked() error {
	list := make([]*pushDevice, 0, len(w.devices))
	for _, d := range w.devices {
		list = append(list, d)
	}
	slices.SortFunc(list, func(a, b *pushDevice) int { return strings.Compare(a.Device, b.Device) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(pushDevicesPath(), data)
}

// keyLocked is the VAPID key, made and saved on first use. The caller holds
// mu.
func (w *webPusher) keyLocked() (*pushnotify.VAPIDKey, error) {
	if w.key != nil {
		return w.key, nil
	}
	if data, err := os.ReadFile(pushKeyPath()); err == nil {
		k, err := pushnotify.ParseVAPIDKey(data)
		if err != nil {
			return nil, err
		}
		w.key = k
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := pushnotify.NewVAPIDKey()
	if err != nil {
		return nil, err
	}
	data, err := k.MarshalPEM()
	if err != nil {
		return nil, err
	}
	if err := writePrivate(pushKeyPath(), data); err != nil {
		return nil, err
	}
	w.key = k
	return k, nil
}

// publicKey is the VAPID public key, base64url, made on first use.
func (w *webPusher) publicKey() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k, err := w.keyLocked()
	if err != nil {
		return "", err
	}
	return k.PublicKey(), nil
}

// register adds or replaces a phone.
func (w *webPusher) register(d pushDevice) (replaced bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if _, err := w.keyLocked(); err != nil {
		return false, err
	}
	_, replaced = w.devices[d.Device]
	if !replaced && len(w.devices) >= pushMaxDevices {
		return false, errPushFull
	}
	prev := w.devices[d.Device]
	w.devices[d.Device] = &d
	if err := w.saveLocked(); err != nil {
		if prev != nil {
			w.devices[d.Device] = prev
		} else {
			delete(w.devices, d.Device)
		}
		return false, err
	}
	return replaced, nil
}

// errPushFull is a register-push past pushMaxDevices.
var errPushFull = errors.New("too many phones")

// remove drops a phone and reports whether it was there.
func (w *webPusher) remove(device string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if _, ok := w.devices[device]; !ok {
		return false, nil
	}
	delete(w.devices, device)
	return true, w.saveLocked()
}

// list is a copy of the phones, by name.
func (w *webPusher) list() []pushDevice {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	out := make([]pushDevice, 0, len(w.devices))
	for _, d := range w.devices {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b pushDevice) int { return strings.Compare(a.Device, b.Device) })
	return out
}

// pushSignature is what an open item shows on a phone that can change while
// it stays open: a new hold gives it a request id, options and risk to answer
// with. A summary that changes under the same prompt is not sent again.
func pushSignature(it AttentionItem) string {
	return it.Kind + "\x00" + it.RequestID + "\x00" + strings.Join(it.Options, ",") + "\x00" + strings.Join(it.Risk, ",")
}

// note folds one attention event in. It never blocks.
func (w *webPusher) note(action string, it AttentionItem) {
	cfg := w.n.cfg.Load()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if action == AttentionClosed {
		// A close goes to every phone that wants the kind, even with
		// notifications off and for an item this run did not push: the
		// open may have gone before a restart. It only takes back what a
		// phone shows.
		delete(w.sent, it.ID)
		w.enqueueLocked(it, pushClose)
		return
	}
	if cfg == nil || !cfg.On() {
		return
	}
	sig := pushSignature(it)
	if prev, ok := w.sent[it.ID]; ok && prev == sig {
		return
	}
	w.sent[it.ID] = sig
	w.enqueueLocked(it, pushOpen)
}

// enqueueLocked builds the payload once and queues it for every phone that
// wants the kind. The caller holds mu.
func (w *webPusher) enqueueLocked(it AttentionItem, typ string) {
	if len(w.devices) == 0 {
		return
	}
	machine := w.n.d.manager.HostName()
	p := pushPayload{
		V: 1, Type: typ, ID: it.ID, Kind: it.Kind, Session: it.Session, Window: it.Window,
		Host: it.Host, Harness: it.Harness, Name: it.Name, Summary: it.Summary,
		Risk: it.Risk, RequestID: it.RequestID, Options: it.Options, Machine: machine,
	}
	if typ == pushClose {
		// A close needs only what names the item.
		p = pushPayload{V: 1, Type: typ, ID: it.ID, Kind: it.Kind, Session: it.Session, Window: it.Window, Host: it.Host, Machine: machine}
	}
	data, ok := fitPayload(p)
	if !ok {
		log.Printf("[NOTIFY] item %s: the push payload does not fit %d bytes", it.ID, pushnotify.MaxPayload)
		return
	}
	urgency := pushnotify.UrgencyNormal
	if typ == pushOpen && slices.Contains(pushDefaultKinds, it.Kind) {
		urgency = pushnotify.UrgencyHigh
	}
	job := pushJob{payload: data, urgency: urgency, topic: pushnotify.Topic(machine + "\x00" + it.ID), itemID: it.ID, typ: typ}
	cfg := w.n.cfg.Load()
	now := time.Now()
	for name, dev := range w.devices {
		if !dev.wants(it.Kind) {
			continue
		}
		if typ == pushOpen && cfg != nil {
			if limit := cfg.HourlyCap(); limit > 0 {
				times := w.hour[name]
				cut := 0
				for cut < len(times) && now.Sub(times[cut]) >= time.Hour {
					cut++
				}
				times = times[cut:]
				if len(times) >= limit {
					w.hour[name] = times
					log.Printf("[NOTIFY] item %s not pushed to %s: %d pushes in the last hour is the max_per_hour limit", it.ID, name, limit)
					continue
				}
				w.hour[name] = append(times, now)
			}
		}
		w.workerLocked(name).put(job)
	}
}

// fitPayload marshals p within MaxPayload, cutting the summary, then the
// options and the risk, if it has to.
func fitPayload(p pushPayload) ([]byte, bool) {
	for range 4 {
		data, err := json.Marshal(p)
		if err != nil {
			return nil, false
		}
		if len(data) <= pushnotify.MaxPayload {
			return data, true
		}
		switch {
		case len(p.Summary) > 64:
			p.Summary = clipUTF8(p.Summary, 64)
		case len(p.Options) > 0:
			p.Options = nil
		default:
			p.Risk = nil
		}
	}
	return nil, false
}

// clipUTF8 cuts s to at most n bytes on a rune boundary.
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// put queues a job, dropping the oldest when the queue is full.
func (pw *pushWorker) put(j pushJob) {
	for {
		select {
		case pw.ch <- j:
			return
		default:
		}
		select {
		case <-pw.ch:
		default:
		}
	}
}

// workerLocked is the phone's worker, started on first use. The caller holds
// mu.
func (w *webPusher) workerLocked(device string) *pushWorker {
	if pw, ok := w.workers[device]; ok {
		return pw
	}
	pw := &pushWorker{ch: make(chan pushJob, pushDeviceQueue)}
	w.workers[device] = pw
	w.wg.Add(1)
	go w.run(device, pw)
	return pw
}

// run sends one phone's pushes in order until the daemon stops.
func (w *webPusher) run(device string, pw *pushWorker) {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		case j := <-pw.ch:
			w.deliver(device, j)
		}
	}
}

// deliver sends one push, with retries, and records how it went.
func (w *webPusher) deliver(device string, j pushJob) {
	for attempt := 0; ; attempt++ {
		w.mu.Lock()
		dev, ok := w.devices[device]
		var sub pushnotify.Subscription
		if ok {
			sub = dev.Subscription
		}
		key, kerr := w.keyLocked()
		w.mu.Unlock()
		if !ok {
			return
		}
		if kerr != nil {
			log.Printf("[NOTIFY] no VAPID key, so nothing is pushed: %v", kerr)
			return
		}
		cfg := w.n.cfg.Load()
		if cfg == nil {
			cfg = &config.NotifyConfig{}
		}
		subject := cfg.WebPushSubject(w.n.d.manager.HostName())
		err := w.n.client.Load().SendWebPush(w.ctx, key, pushnotify.Push{
			Sub: sub, Payload: j.payload, Urgency: j.urgency, Topic: j.topic, Subject: subject,
		}, cfg.WebPushInsecure())
		host := hostOfEndpoint(sub.Endpoint)
		if err == nil {
			w.record(device, "")
			LogBasic("[NOTIFY] webpush (%s) sent %s of item %s to %s", host, j.typ, j.itemID, device)
			return
		}
		if errors.Is(err, pushnotify.ErrGone) {
			log.Printf("[NOTIFY] webpush (%s): the push service no longer knows %s, so it is removed", host, device)
			if _, rerr := w.remove(device); rerr != nil {
				log.Printf("[NOTIFY] cannot save the registered phones: %v", rerr)
			}
			return
		}
		var retry *pushnotify.RetryableError
		if !errors.As(err, &retry) || attempt >= len(pushRetryDelays) {
			log.Printf("[NOTIFY] webpush (%s) failed for item %s to %s: %v", host, j.itemID, device, err)
			w.record(device, err.Error())
			return
		}
		wait := pushRetryDelays[attempt]
		if retry.After > wait && retry.After <= 30*time.Second {
			wait = retry.After
		}
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// record keeps how the last push to a phone went. It is not saved at once:
// the next register or remove writes it.
func (w *webPusher) record(device, errText string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if d, ok := w.devices[device]; ok {
		if errText == "" {
			d.LastOK, d.LastError = time.Now().Unix(), ""
		} else {
			d.LastError = errText
		}
	}
}

// hostOfEndpoint is the host of an endpoint, the one part of it a log names.
func hostOfEndpoint(raw string) string {
	if origin, err := pushnotify.CheckEndpoint(raw, true); err == nil {
		_, host, _ := strings.Cut(origin, "://")
		return host
	}
	return "?"
}

// stop ends the workers. Pushes still queued are dropped.
func (w *webPusher) stop() {
	w.cancel()
	w.wg.Wait()
}
