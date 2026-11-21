package session

import (
	"bytes"
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

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/pushnotify"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

var pushDefaultKinds = []string{AttentionApproval, AttentionPlan, AttentionAsk, AttentionQuestion}

const pushMaxDevices = 16

const pushDeviceQueue = 64

var pushRetryDelays = []time.Duration{time.Second, 4 * time.Second, 15 * time.Second}

const pushTTLWaiting = 3600

const pushTTLOther = pushnotify.PushTTL

type pushDevice struct {
	Device string   `json:"device"`
	Kinds  []string `json:"kinds"`
	pushnotify.Subscription
	Created   int64  `json:"created"`
	LastOK    int64  `json:"last_ok,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (p *pushDevice) wants(kind string) bool { return slices.Contains(p.Kinds, kind) }

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
	Truncated bool     `json:"truncated,omitempty"`
}

const (
	pushOpen  = "open"
	pushClose = "close"
)

type pushJob struct {
	payload []byte
	urgency string
	topic   string
	ttl     int
	itemID  string
	typ     string
}

type webPusher struct {
	n *pushNotifier

	mu      sync.Mutex
	devices map[string]*pushDevice
	loaded  bool
	key     *pushnotify.VAPIDKey
	sent    map[string]string
	hour    map[string][]time.Time
	workers map[string]*pushWorker
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

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

func pushDir() string { return filepath.Join(getResurrectionDir(), "push") }

var pushChmodWarn sync.Once

func writePushFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs x, and 0700 opens it to its owner only
		pushChmodWarn.Do(func() { LogError("Warning: could not make %s private: %v", dir, err) })
	}
	return atomicfile.Write(path, data, 0o600, false)
}

func pushDevicesPath() string { return filepath.Join(pushDir(), "subscriptions.json") }
func pushKeyPath() string     { return filepath.Join(pushDir(), "vapid.pem") }

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
	if err := jsonx.Unmarshal(data, &list); err != nil {
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
	return writePushFile(pushDevicesPath(), data)
}

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
	if err := writePushFile(pushKeyPath(), data); err != nil {
		return nil, err
	}
	w.key = k
	return k, nil
}

func (w *webPusher) publicKey() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k, err := w.keyLocked()
	if err != nil {
		return "", err
	}
	return k.PublicKey(), nil
}

func (w *webPusher) register(d pushDevice, mayReplace bool) (replaced bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if _, err := w.keyLocked(); err != nil {
		return false, err
	}
	_, replaced = w.devices[d.Device]
	if replaced && !mayReplace {
		return false, errPushExists
	}
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

var errPushFull = errors.New("too many phones")

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

func (w *webPusher) list() []pushDevice {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	out := make([]pushDevice, 0, len(w.devices))
	for _, d := range w.devices {
		out = append(out, *d)
	}
	sortx.SortFunc(out, func(a, b pushDevice) int { return strings.Compare(a.Device, b.Device) })
	return out
}

func pushSignature(it AttentionItem) string {
	return it.Kind + "\x00" + it.RequestID + "\x00" + strings.Join(it.Options, ",") + "\x00" + strings.Join(it.Risk, ",")
}

func (w *webPusher) note(action string, it AttentionItem) {
	cfg := w.n.cfg.Load()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if action == AttentionClosed {
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
		p = pushPayload{V: 1, Type: typ, ID: it.ID, Kind: it.Kind, Session: it.Session, Window: it.Window, Host: it.Host, Machine: machine}
	}
	data, cut := fitPayload(p)
	if cut {
		log.Printf("[NOTIFY] item %s: the push payload was cut to fit %d bytes", it.ID, pushnotify.MaxPayload)
	}
	urgency := pushnotify.UrgencyNormal
	ttl := pushTTLOther
	if slices.Contains(pushDefaultKinds, it.Kind) {
		ttl = pushTTLWaiting
		if typ == pushOpen {
			urgency = pushnotify.UrgencyHigh
		}
	}
	job := pushJob{payload: data, urgency: urgency, topic: pushnotify.Topic(machine + "\x00" + it.ID), ttl: ttl, itemID: it.ID, typ: typ}
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

func fitPayload(p pushPayload) ([]byte, bool) {
	marshal := func(p pushPayload) []byte {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(p); err != nil {
			return nil
		}
		return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	}
	data := marshal(p)
	if len(data) <= pushnotify.MaxPayload {
		return data, false
	}
	p.Truncated = true
	if len(p.Summary) > 64 {
		p.Summary = pushnotify.Clip(p.Summary, 64)
		if data = marshal(p); len(data) <= pushnotify.MaxPayload {
			return data, true
		}
	}
	p.Risk = nil
	for limit := 512; ; limit /= 2 {
		if data = marshal(p); len(data) <= pushnotify.MaxPayload || limit == 0 {
			return data, true
		}
		for _, f := range []*string{&p.Summary, &p.Name, &p.Session, &p.Window, &p.Host, &p.Harness, &p.Machine} {
			*f = pushnotify.Clip(*f, limit)
		}
	}
}

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
		subject := cfg.WebPushSubject()
		err := w.n.client.Load().SendWebPush(w.ctx, key, pushnotify.Push{
			Sub: sub, Payload: j.payload, Urgency: j.urgency, Topic: j.topic, Subject: subject, TTL: j.ttl,
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
			w.n.d.attention.notePhone(device, "The push service ("+host+") no longer knows this phone, so tuios removed it. Register the phone again to get pushes.")
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

func hostOfEndpoint(raw string) string {
	if origin, err := pushnotify.CheckEndpoint(raw, true); err == nil {
		_, host, _ := strings.Cut(origin, "://")
		return host
	}
	return "?"
}

func (w *webPusher) stop() {
	w.cancel()
	w.wg.Wait()
}
