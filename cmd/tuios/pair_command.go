package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/hostsetup"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"rsc.io/qr"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	pairProtocol     = "tuios-pair-v1"
	pairPath         = "/v1/pair"
	pairBodyLimit    = 8 << 10
	pairFailureLimit = 3
	pairFailurePause = 200 * time.Millisecond
	pairHeaderLimit  = 4 << 10
	pairConnDeadline = 10 * time.Second
	pairDrainLimit   = 64 << 10
	pairDrainWait    = time.Second
	pairConnLimit    = 16
	pairMaxTimeout   = time.Hour
	pairLANLimit     = 3
)

type pairOptions struct {
	name           string
	machine        string
	listen         string
	listenAll      bool
	authorizedKeys string
	allow          []string
	yes            bool
	timeout        time.Duration
	jsonOut        bool
	advertiseSSH   []string
	advertisePair  []string
	hostKey        string
	sshPort        int
	command        string
	acceptLocal    bool
}

var pairDefaultAllow = []string{config.LinkAllowList, config.LinkAllowMail}

func newPairCommand() *cobra.Command {
	var o pairOptions
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Show a QR code that adds this machine to a phone",
		Long: `Show a QR code that adds this machine to a phone.

The tuios phone app is not released yet. An Android app is in private
testing. An iOS app comes later. To use tuios on a phone today, open
tuios-web in the browser on the phone. Keep tuios-web on localhost and reach
it over Tailscale or an ssh tunnel, or start it with a password. To talk to
agents, use Collie (https://github.com/AltanS/collie). Collie has its own
pairing. It does not use tuios pair.

Scan the code with the tuios app on the phone. The phone sends its public
key. tuios shows the device name and the key fingerprint and asks you to
accept the key. Then it adds the key to your authorized_keys file.

The key can only run "tuios stdio-proxy --as DEVICE". tuios writes a new
[hosts.DEVICE] table to config.toml first, with allow set to --allow. The
default is list and mail. Then it adds the key. A device name that is in use
here is refused: the name of a [hosts] table, of a key that opens a link
already, or of this machine.

The code works one time. It stops when one phone pairs, when the time runs
out, or after three wrong requests. The phone must reach this machine on the
network, for example on the same Wi-Fi network or on your tailnet. A request
from this machine, or from a machine in your [hosts] table, stops the
pairing.

The pairing listener uses only the addresses in the code. These are the
tailnet address and the private LAN addresses, or the address you give with
--listen. To listen on every interface or on a public address, add
--listen-all.

Anyone who sees the code can pair until the phone does. Compare the check
code and the fingerprint on the phone with the ones here before you accept.`,
		Example: `  # Pair a phone. It can read listings and send and read mail
  tuios pair --name phone

  # Pair a phone that can also start programs and type into panes
  tuios pair --name phone --allow list,mail,open,write

  # Listen on one address, and give the phone the tailnet name
  tuios pair --listen 100.64.0.7:7420 --advertise-pair studio.example.ts.net:7420`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			startPprofServer()
			if err := startCPUProfile(); err != nil {
				return err
			}
			defer stopCPUProfile()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runPair(ctx, o, cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "Name for the device. It replaces the name the phone sends")
	f.StringVar(&o.machine, "machine", "", "Name of this machine in the code, which the phone shows (default the host name)")
	f.StringVar(&o.listen, "listen", "", "Address for the pairing listener, as HOST:PORT. Port 0 picks a free port")
	f.StringVar(&o.authorizedKeys, "authorized-keys", "", "File to add the key to (default ~/.ssh/authorized_keys)")
	f.StringSliceVar(&o.allow, "allow", nil, "Capabilities for the new [hosts.DEVICE] table: list, mail, open, write, respond, files (default list,mail)")
	f.BoolVar(&o.yes, "yes", false, "Accept the key without a question. Use it for tests only")
	f.BoolVar(&o.listenAll, "listen-all", false, "Let the listener use every interface (0.0.0.0 or [::]) or a public address")
	f.BoolVar(&o.acceptLocal, "accept-local", false, "Accept a request from this machine, for an emulator or a userspace tailscaled. Programs here that can read the code can then pair")
	f.DurationVar(&o.timeout, "timeout", 5*time.Minute, "Time the code works")
	addJSONFlag(cmd, &o.jsonOut, "Print the link and the result as JSON lines")
	f.StringSliceVar(&o.advertiseSSH, "advertise-ssh", nil, "The ssh addresses for the phone, as HOST:PORT. They replace the addresses tuios finds")
	f.StringSliceVar(&o.advertisePair, "advertise-pair", nil, "The pairing addresses for the phone, as HOST:PORT. They replace the addresses tuios finds")
	f.StringVar(&o.hostKey, "host-key", "", "The ssh host public key to give the phone (default the first of /etc/ssh/ssh_host_{ed25519,ecdsa,rsa}_key.pub)")
	f.IntVar(&o.sshPort, "ssh-port", 22, "Port of the ssh server, for the addresses tuios finds")
	f.StringVar(&o.command, "command", "", "Path of tuios in the forced command (default the tuios on PATH when it is this tuios)")
	return cmd
}

type pairHostKey struct {
	line        string
	fingerprint string
}

type pairRequest struct {
	Device string `json:"device"`
	Key    string `json:"key"`
	MAC    string `json:"mac"`
}

type pairReply struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Device  string `json:"device,omitempty"`
	User    string `json:"user,omitempty"`
	Command string `json:"command,omitempty"`
	HostKey string `json:"host_key,omitempty"`
	MAC     string `json:"mac,omitempty"`
	Check   string `json:"check,omitempty"`
}

type pairResult struct {
	Device         string   `json:"device"`
	KeyType        string   `json:"key_type"`
	Fingerprint    string   `json:"fingerprint"`
	Check          string   `json:"check"`
	Allow          []string `json:"allow"`
	AuthorizedKeys string   `json:"authorized_keys"`
	Config         string   `json:"config"`
	configNote     string
}

type pairAddr struct {
	advertise string
	bind      string
	public    bool
}

func runPair(ctx context.Context, o pairOptions, stdout, stderr io.Writer, stdin io.Reader) error {
	if o.timeout <= 0 || o.timeout > pairMaxTimeout {
		return errfmt.Errorf("--timeout must be more than 0 and at most %s", pairMaxTimeout)
	}
	if o.name != "" {
		if err := pairDeviceNameOK(o.name); err != nil {
			return err
		}
	}
	allow, err := pairAllowList(o.allow)
	if err != nil {
		return err
	}
	if len(allow) == 0 {
		allow = slices.Clone(pairDefaultAllow)
	}
	configPath, err := config.GetConfigPath()
	if err != nil {
		return errfmt.Errorf("cannot find config.toml, where the policy of the device goes: %w", err)
	}
	hosts, err := pairHosts(configPath)
	if err != nil {
		return err
	}
	keysPath := o.authorizedKeys
	if keysPath == "" {
		if keysPath = hostsetup.AuthorizedKeysPath(); keysPath == "" {
			return errors.New("cannot find your home folder. Use --authorized-keys")
		}
	}
	if keysPath, err = filepath.Abs(keysPath); err != nil {
		return err
	}
	if err := hostsetup.CheckAuthorizedKeysModes(keysPath, stderr); err != nil {
		return err
	}
	hostKey, err := loadPairHostKey(o.hostKey)
	if err != nil {
		return err
	}
	tuiosPath, err := pairTuiosPath(o.command, stderr)
	if err != nil {
		return err
	}
	me, err := user.Current()
	if err != nil {
		return errfmt.Errorf("cannot find your user name: %w", err)
	}
	machine := o.machine
	if machine != "" {
		if err := session.ValidLinkPeerName(machine); err != nil {
			return errfmt.Errorf("--machine: %w", err)
		}
	} else {
		machine, _ = os.Hostname()
		machine, _, _ = strings.Cut(machine, ".")
	}

	tailnet := sync.OnceValue(func() []federation.TailnetMachine {
		opt, _ := tailnetOptions()
		tctx, cancel := context.WithTimeout(ctx, tailnetProbeTimeout)
		defer cancel()
		machines, err := federation.TailnetMachines(tctx, opt)
		if err != nil {
			return nil
		}
		return machines
	})

	ownNames := []string{machine}
	if h, err := os.Hostname(); err == nil {
		short, _, _ := strings.Cut(h, ".")
		ownNames = append(ownNames, h, short)
	}
	if o.name != "" {
		if why := pairNameInUse(o.name, hosts, keysPath, ownNames); why != "" {
			return errfmt.Errorf("--name %s: %s. Give the device another name", o.name, why)
		}
	}
	peers := pairPeerAddrs(ctx, hosts, tailnet)

	var found []pairAddr
	if len(o.advertiseSSH) == 0 || o.listen == "" || (len(o.advertisePair) == 0 && listensEverywhere(o.listen)) {
		found = discoverPairAddrs(tailnet())
	}
	sshAddrs := o.advertiseSSH
	if len(sshAddrs) == 0 {
		for _, a := range found {
			sshAddrs = append(sshAddrs, net.JoinHostPort(a.advertise, strconv.Itoa(o.sshPort)))
		}
	}
	if len(sshAddrs) == 0 {
		return errors.New("tuios found no network address for this machine. Use --advertise-ssh")
	}
	for _, a := range append(slices.Clone(sshAddrs), o.advertisePair...) {
		if _, _, err := net.SplitHostPort(a); err != nil || strings.ContainsAny(a, ",&#?/ ") {
			return errfmt.Errorf("%q is not an address. Write HOST:PORT", a)
		}
	}

	if listensEverywhere(o.listen) {
		if !o.listenAll {
			return errfmt.Errorf("--listen %s listens on every interface. Give one address of this machine, or add --listen-all", o.listen)
		}
		fmt.Fprintf(stderr, "Warning: the pairing listener listens on every interface of this machine (%s).\n", o.listen)
	}
	listeners, pairAddrs, err := openPairListeners(o.listen, o.listenAll, found, func() []pairAddr { return discoverPairAddrs(tailnet()) }, stderr)
	if err != nil {
		return err
	}
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	if len(o.advertisePair) > 0 {
		pairAddrs = o.advertisePair
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return errfmt.Errorf("cannot make a pairing token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	link := pairURL(machine, me.Username, sshAddrs, pairAddrs, hostKey.fingerprint, token)
	deadline := time.Now().Add(o.timeout)

	listenOn := make([]string, 0, len(listeners))
	for _, l := range listeners {
		listenOn = append(listenOn, l.Addr().String())
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if o.jsonOut {
		_ = enc.Encode(map[string]any{
			"url":         link,
			"listen":      listenOn,
			"ssh":         sshAddrs,
			"pair":        pairAddrs,
			"fingerprint": hostKey.fingerprint,
			"expires_at":  deadline.UTC().Format(time.RFC3339),
		})
		if f, ok := stderr.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			fmt.Fprint(stderr, renderQR(link, os.Getenv("NO_COLOR") == ""))
		}
	} else {
		fmt.Fprintln(stdout, "Scan this code with the tuios app on your phone.")
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, renderQR(link, os.Getenv("NO_COLOR") == ""))
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Or open this link on the phone:")
		fmt.Fprintln(stdout, link)
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "The phone connects to ssh at %s.\n", strings.Join(sshAddrs, ", "))
		fmt.Fprintf(stdout, "Host key: %s\n", hostKey.fingerprint)
		fmt.Fprintf(stdout, "The code works one time, until %s. Press Ctrl+C to stop.\n", deadline.Format("15:04:05"))
	}

	srv := &pairServer{
		token:     tokenBytes,
		deadline:  deadline,
		name:      o.name,
		allow:     allow,
		config:    configPath,
		ownNames:  ownNames,
		peers:     peers,
		local:     o.acceptLocal,
		yes:       o.yes,
		keysPath:  keysPath,
		hostKey:   hostKey,
		user:      me.Username,
		tuiosPath: tuiosPath,
		stdin:     stdin,
		prompt:    stderr,
		done:      make(chan struct{}),
	}
	result, err := srv.serve(ctx, listeners)
	if err != nil {
		if o.jsonOut {
			_ = enc.Encode(map[string]any{"ok": false, "error": err.Error()})
		}
		return err
	}
	if o.jsonOut {
		_ = enc.Encode(map[string]any{
			"ok":              true,
			"device":          result.Device,
			"key_type":        result.KeyType,
			"fingerprint":     result.Fingerprint,
			"check":           result.Check,
			"allow":           result.Allow,
			"authorized_keys": result.AuthorizedKeys,
			"config":          result.Config,
		})
		if result.configNote != "" {
			fmt.Fprintln(stderr, result.configNote)
		}
		return nil
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "The device %s is paired.\n", result.Device)
	fmt.Fprintf(stdout, "Key: %s %s\n", result.KeyType, result.Fingerprint)
	fmt.Fprintf(stdout, "The key is in %s. It can only open a tuios link as %s.\n", result.AuthorizedKeys, result.Device)
	if result.configNote != "" {
		fmt.Fprintln(stdout, result.configNote)
	}
	return nil
}

func pairDeviceNameOK(name string) error {
	if err := session.ValidLinkPeerName(name); err != nil {
		return err
	}
	return federation.ValidHostName(name)
}

func pairHosts(path string) (map[string]config.HostConfig, error) {
	hosts, err := config.HostsInFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]config.HostConfig{}, nil
	}
	if err != nil {
		return nil, errfmt.Errorf("cannot read %s, where the policy of the device goes: %w. Correct the file and run tuios pair again", path, err)
	}
	return hosts, nil
}

func pairNameInUse(device string, hosts map[string]config.HostConfig, keysPath string, ownNames []string) string {
	for key := range hosts {
		if strings.EqualFold(key, device) {
			return fmt.Sprintf("config.toml has a [hosts.%s] table", key)
		}
	}
	for _, name := range pairPinnedNames(keysPath) {
		if strings.EqualFold(name, device) {
			return fmt.Sprintf("a key in %s opens links as %s already", keysPath, name)
		}
	}
	for _, name := range ownNames {
		if name != "" && strings.EqualFold(name, device) {
			return "it is the name of this machine"
		}
	}
	return ""
}

var pairPinnedNamePattern = regexp.MustCompile(`stdio-proxy\s+--as[ =]([A-Za-z0-9._-]+)`)

func pairPinnedNames(keysPath string) []string {
	data, err := os.ReadFile(keysPath) //nolint:gosec // the file the person names
	if err != nil {
		return nil
	}
	var names []string
	for line := range strings.SplitSeq(string(data), "\n") {
		_, _, opts, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			continue
		}
		for _, opt := range opts {
			if !strings.HasPrefix(strings.ToLower(opt), "command=") {
				continue
			}
			if m := pairPinnedNamePattern.FindStringSubmatch(opt); m != nil {
				names = append(names, m[1])
			}
		}
	}
	return names
}

func pairPeerAddrs(ctx context.Context, hosts map[string]config.HostConfig, tailnet func() []federation.TailnetMachine) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	type lookup struct{ name, host string }
	var todo []lookup
	for name, h := range hosts {
		if strings.TrimSpace(h.Addr) == "" {
			continue
		}
		host := pairAddrHost(h.Addr)
		if a, err := netip.ParseAddr(host); err == nil {
			out[a.Unmap()] = name
			continue
		}
		todo = append(todo, lookup{name, host})
	}
	if len(todo) == 0 {
		return out
	}
	for _, m := range tailnet() {
		if m.Self || m.IP == "" {
			continue
		}
		a, err := netip.ParseAddr(m.IP)
		if err != nil {
			continue
		}
		for _, l := range todo {
			if strings.EqualFold(l.host, m.Name) || strings.EqualFold(strings.TrimSuffix(l.host, "."), m.DNSName) || strings.EqualFold(l.name, m.Name) {
				out[a.Unmap()] = l.name
			}
		}
	}
	lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, l := range todo {
		wg.Go(func() {
			addrs, err := net.DefaultResolver.LookupNetIP(lctx, "ip", l.host)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, a := range addrs {
				out[a.Unmap()] = l.name
			}
		})
	}
	wg.Wait()
	return out
}

func pairAddrHost(addr string) string {
	addr = strings.TrimSpace(addr)
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		addr = addr[i+1:]
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return strings.Trim(addr, "[]")
}

func pairAllowList(in []string) ([]string, error) {
	var out []string
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !slices.Contains(config.LinkCapabilities, c) {
			return nil, errfmt.Errorf("--allow: %q is not a capability. Use %s", c, strings.Join(config.LinkCapabilities, ", "))
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

func loadPairHostKey(path string) (pairHostKey, error) {
	candidates := []string{path}
	if path == "" {
		candidates = []string{
			"/etc/ssh/ssh_host_ed25519_key.pub",
			"/etc/ssh/ssh_host_ecdsa_key.pub",
			"/etc/ssh/ssh_host_rsa_key.pub",
		}
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p) //nolint:gosec // a public key file the person names
		if err != nil {
			if path == "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return pairHostKey{}, errfmt.Errorf("cannot read the host key: %w", err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return pairHostKey{}, errfmt.Errorf("%s is not an ssh public key: %w", p, err)
		}
		return pairHostKey{
			line:        strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
			fingerprint: ssh.FingerprintSHA256(pub),
		}, nil
	}
	return pairHostKey{}, errors.New("sshd has no host keys in /etc/ssh. sshd is probably not set up, or it never started. " +
		"To make the host keys, run: sudo ssh-keygen -A. Then start sshd. " +
		"--host-key must name a host key of sshd, such as /etc/ssh/ssh_host_ed25519_key.pub. " +
		"Do not give it a user key such as ~/.ssh/id_ed25519.pub")
}

func pairTuiosPath(given string, stderr io.Writer) (string, error) {
	p := given
	if p == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", errfmt.Errorf("cannot find the path of tuios: %w. Use --command", err)
		}
		p = exe
		onPath, err := exec.LookPath("tuios")
		if err == nil {
			onPath, err = filepath.Abs(onPath)
		}
		if err == nil && pairSameFile(onPath, exe) {
			p = onPath
		} else {
			fmt.Fprintf(stderr, "Warning: tuios is not on PATH, or the tuios on PATH is another binary. The key runs %s. When that file moves, for example after an upgrade, the phone cannot connect. Use --command to give a path that stays the same.\n", exe)
		}
		if strings.Contains(p, "/nix/store/") || strings.Contains(p, "/Cellar/") {
			fmt.Fprintf(stderr, "Warning: %s changes when tuios is upgraded. Use --command to give a path that stays the same.\n", p)
		}
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(p, "\"\\\n\r\t'") {
		return "", errfmt.Errorf("the path %q holds a quote, a backslash or a control character. Use --command with another path", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", errfmt.Errorf("the path %q holds a control character. Use --command with another path", p)
		}
	}
	plain := true
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+-", r)) {
			plain = false
			break
		}
	}
	if !plain {
		p = "'" + p + "'"
	}
	return p, nil
}

func pairSameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

func discoverPairAddrs(machines []federation.TailnetMachine) []pairAddr {
	var out []pairAddr
	for _, a := range federation.LocalAddrs(machines, pairLANLimit) {
		out = append(out, pairAddr{advertise: a.Host, bind: a.Bind, public: a.Public})
	}
	return out
}

func listensEverywhere(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func openPairListeners(listen string, listenAll bool, found []pairAddr, discover func() []pairAddr, stderr io.Writer) ([]net.Listener, []string, error) {
	if listen != "" {
		if host, _, err := net.SplitHostPort(listen); err == nil && !listenAll && federation.PublicIP(net.ParseIP(host)) {
			return nil, nil, errfmt.Errorf("--listen %s is a public address. Give a private or tailnet address of this machine, or add --listen-all", listen)
		}
		l, err := net.Listen("tcp", listen)
		if err != nil {
			return nil, nil, errfmt.Errorf("cannot listen on %s: %w", listen, err)
		}
		host, port, _ := net.SplitHostPort(l.Addr().String())
		var adv []string
		ip := net.ParseIP(host)
		if !listenAll && federation.PublicIP(ip) {
			_ = l.Close()
			return nil, nil, errfmt.Errorf("--listen %s is a public address. Give a private or tailnet address of this machine, or add --listen-all", listen)
		}
		if ip != nil && ip.IsUnspecified() {
			if !listenAll {
				_ = l.Close()
				return nil, nil, errfmt.Errorf("--listen %s listens on every interface. Give one address of this machine, or add --listen-all", listen)
			}
			if !listensEverywhere(listen) {
				fmt.Fprintf(stderr, "Warning: the pairing listener listens on every interface of this machine (%s).\n", listen)
			}
			if found == nil {
				found = discover()
			}
			for _, a := range found {
				adv = append(adv, net.JoinHostPort(a.advertise, port))
			}
		}
		if len(adv) == 0 {
			adv = []string{l.Addr().String()}
		}
		return []net.Listener{l}, adv, nil
	}
	if len(found) == 0 {
		return nil, nil, errors.New("tuios found no network address for this machine. Use --listen and --advertise-pair")
	}
	var ls []net.Listener
	var adv []string
	var skipped []string
	port := "0"
	for _, a := range found {
		if a.public && !listenAll {
			skipped = append(skipped, a.bind)
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(a.bind, port))
		if err != nil && port != "0" {
			l, err = net.Listen("tcp", net.JoinHostPort(a.bind, "0"))
		}
		if err != nil {
			continue
		}
		_, p, _ := net.SplitHostPort(l.Addr().String())
		if port == "0" {
			port = p
		}
		ls = append(ls, l)
		adv = append(adv, net.JoinHostPort(a.advertise, p))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stderr, "The pairing listener does not use the public address %s. Add --listen-all to use it.\n", strings.Join(skipped, ", "))
	}
	if len(ls) == 0 {
		return nil, nil, errors.New("tuios cannot listen on any private or tailnet address of this machine. Use --listen")
	}
	return ls, adv, nil
}

func pairURL(machine, user string, ssh, pair []string, fp, token string) string {
	esc := func(s string) string {
		s = url.QueryEscape(s)
		return strings.NewReplacer("%3A", ":", "%2C", ",", "%2F", "/").Replace(s)
	}
	join := func(list []string) string {
		parts := make([]string, len(list))
		for i, a := range list {
			parts[i] = esc(a)
		}
		return strings.Join(parts, ",")
	}
	return "tuios://pair?v=1" +
		"&m=" + esc(machine) +
		"&u=" + esc(user) +
		"&s=" + join(ssh) +
		"&p=" + join(pair) +
		"&fp=" + esc(fp) +
		"&t=" + token
}

func pairMAC(token []byte, msg string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func pairCheckCode(token []byte, key string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(pairProtocol + "-check\n" + key))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(m.Sum(nil)[:4])%1000000)
}

type pairServer struct {
	token     []byte
	deadline  time.Time
	name      string
	allow     []string
	config    string
	ownNames  []string
	peers     map[netip.Addr]string
	local     bool
	yes       bool
	keysPath  string
	hostKey   pairHostKey
	user      string
	tuiosPath string
	stdin     io.Reader
	prompt    io.Writer

	busy     sync.Mutex
	commit   sync.Mutex
	failures int
	used     bool
	result   *pairResult
	err      error
	done     chan struct{}
	doneOnce sync.Once
}

func (s *pairServer) stop(err error) {
	s.commit.Lock()
	defer s.commit.Unlock()
	s.finish(nil, err)
}

func (s *pairServer) finish(res *pairResult, err error) {
	s.doneOnce.Do(func() {
		s.result, s.err = res, err
		close(s.done)
	})
}

func (s *pairServer) serve(ctx context.Context, listeners []net.Listener) (*pairResult, error) {
	var conns sync.WaitGroup
	slots := make(chan struct{}, pairConnLimit)
	for _, l := range listeners {
		go func(l net.Listener) {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				select {
				case slots <- struct{}{}:
				default:
					_ = c.Close()
					continue
				}
				conns.Add(1)
				go func() {
					defer func() { <-slots; conns.Done() }()
					s.serveConn(c)
				}()
			}
		}(l)
	}
	timer := time.NewTimer(time.Until(s.deadline))
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		s.stop(errors.New("no phone paired before the time ran out. Run tuios pair again"))
	case <-ctx.Done():
		s.stop(errors.New("pairing is stopped"))
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	waited := make(chan struct{})
	go func() { conns.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(pairConnDeadline):
	}
	return s.result, s.err
}

func (s *pairServer) serveConn(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(pairConnDeadline))
	br := bufio.NewReaderSize(c, 1024)
	req := readPairHTTP(br, c)
	if req.closed {
		return
	}
	req.remote = c.RemoteAddr().String()
	status, reply, _ := s.answer(req)
	_ = c.SetWriteDeadline(time.Now().Add(pairConnDeadline))
	writePairReply(c, status, reply)
	drainPairConn(c, br)
}

func drainPairConn(c net.Conn, r io.Reader) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	_ = c.SetReadDeadline(time.Now().Add(pairDrainWait))
	_, _ = io.CopyN(io.Discard, r, pairDrainLimit)
}

func (s *pairServer) answer(req pairHTTP) (int, pairReply, *pairResult) {
	if !s.busy.TryLock() {
		return http.StatusTooManyRequests, pairReply{Error: "Another request is in progress. Try again."}, nil
	}
	defer s.busy.Unlock()
	select {
	case <-s.done:
		return http.StatusGone, pairReply{Error: "Pairing is stopped. Run tuios pair again."}, nil
	default:
	}
	if s.used || time.Now().After(s.deadline) {
		return http.StatusGone, pairReply{Error: "This code is used or too old. Run tuios pair again."}, nil
	}
	return s.handle(req)
}

type pairHTTP struct {
	method, path, remote string
	body                 []byte
	bad                  int
	closed               bool
}

func readPairHTTP(br *bufio.Reader, w io.Writer) pairHTTP {
	var req pairHTTP
	read := 0
	var readErr error
	line := func() (string, bool) {
		var b []byte
		for {
			chunk, err := br.ReadSlice('\n')
			read += len(chunk)
			if read > pairHeaderLimit {
				return "", false
			}
			b = append(b, chunk...)
			if err == nil {
				return strings.TrimRight(string(b), "\r\n"), true
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				readErr = err
				return "", false
			}
		}
	}
	first, ok := line()
	if !ok && read == 0 {
		var ne net.Error
		if !errors.As(readErr, &ne) || !ne.Timeout() {
			req.closed = true
			return req
		}
	}
	parts := strings.Fields(first)
	if !ok || len(parts) != 3 || !strings.HasPrefix(parts[2], "HTTP/1.") {
		req.bad = http.StatusBadRequest
		return req
	}
	req.method = parts[0]
	if u, err := url.ParseRequestURI(parts[1]); err == nil {
		req.path = u.Path
	}
	length, expect, chunked := -1, false, false
	for {
		h, ok := line()
		if !ok {
			req.bad = http.StatusRequestHeaderFieldsTooLarge
			return req
		}
		if h == "" {
			break
		}
		name, value, _ := strings.Cut(h, ":")
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || (length >= 0 && n != length) {
				req.bad = http.StatusBadRequest
				return req
			}
			length = n
		case "transfer-encoding":
			chunked = true
		case "expect":
			expect = strings.EqualFold(value, "100-continue")
		}
	}
	switch {
	case req.path != pairPath || req.method != http.MethodPost:
		return req
	case chunked:
		req.bad = http.StatusLengthRequired
		return req
	case length > pairBodyLimit:
		req.bad = http.StatusRequestEntityTooLarge
		return req
	case length < 0:
		req.bad = http.StatusLengthRequired
		return req
	}
	if expect {
		_, _ = io.WriteString(w, "HTTP/1.1 100 Continue\r\n\r\n")
	}
	req.body = make([]byte, length)
	if _, err := io.ReadFull(br, req.body); err != nil {
		req.bad = http.StatusBadRequest
	}
	return req
}

func (s *pairServer) fail(status int, msg string) (int, pairReply, *pairResult) {
	s.failures++
	time.Sleep(pairFailurePause)
	if s.failures >= pairFailureLimit {
		s.used = true
		s.finish(nil, errors.New("too many wrong requests reached the pairing listener, so pairing is stopped. Run tuios pair again"))
		msg += " Too many wrong requests. Pairing is stopped."
	}
	return status, pairReply{Error: msg}, nil
}

func refuse(status int, msg string) (int, pairReply, *pairResult) {
	return status, pairReply{Error: msg}, nil
}

func (s *pairServer) handle(r pairHTTP) (int, pairReply, *pairResult) {
	switch {
	case r.bad == http.StatusBadRequest:
		return refuse(r.bad, "The request is not a complete HTTP request.")
	case r.bad == http.StatusRequestHeaderFieldsTooLarge:
		return refuse(r.bad, "The request headers are too large.")
	case r.path != pairPath:
		return refuse(http.StatusNotFound, "This address does not accept that request.")
	case r.method != http.MethodPost:
		return refuse(http.StatusMethodNotAllowed, "Use POST.")
	case r.bad == http.StatusRequestEntityTooLarge:
		return refuse(r.bad, "The request is too large.")
	case r.bad == http.StatusLengthRequired:
		return refuse(r.bad, "The request needs a Content-Length header.")
	case r.bad != 0:
		return refuse(http.StatusBadRequest, "The request is not a complete HTTP request.")
	}
	var req pairRequest
	if err := jsonx.Unmarshal(r.body, &req); err != nil || req.Device == "" || req.Key == "" || req.MAC == "" {
		return refuse(http.StatusBadRequest, "The request must be JSON with device, key and mac.")
	}
	want := pairMAC(s.token, pairProtocol+"\n"+req.Device+"\n"+req.Key)
	if !hmac.Equal([]byte(want), []byte(req.MAC)) {
		return s.fail(http.StatusForbidden, "The code does not match. Scan the QR code again.")
	}

	if why := s.sourceRefusal(r.remote); why != "" {
		s.used = true
		s.finish(nil, errors.New(why))
		return http.StatusForbidden, pairReply{Error: "This computer does not accept a pairing request from itself or from a machine it links with. Pairing is stopped."}, nil
	}

	device := req.Device
	if s.name != "" {
		device = s.name
	}
	if err := pairDeviceNameOK(device); err != nil {
		return http.StatusBadRequest, pairReply{Error: "The device name is not valid. Use letters, digits, dot, dash and underscore, and start with a letter or a digit."}, nil
	}
	hosts, err := pairHosts(s.config)
	if err != nil {
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot read its config file."}, nil
	}
	if why := pairNameInUse(device, hosts, s.keysPath, s.ownNames); why != "" {
		return http.StatusConflict, pairReply{Error: "The name " + device + " is in use on this computer. Give the phone another name."}, nil
	}
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(req.Key))
	if err != nil || strings.TrimSpace(string(rest)) != "" || strings.ContainsAny(strings.TrimRight(req.Key, "\r\n"), "\r\n") {
		return http.StatusBadRequest, pairReply{Error: "The key is not one OpenSSH public key line."}, nil
	}
	keyType, refusal := pairKeyAccepted(pub)
	if refusal != "" {
		return http.StatusBadRequest, pairReply{Error: refusal}, nil
	}
	present, err := hostsetup.AuthorizedKeysHas(s.keysPath, pub)
	if err != nil {
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot read its authorized keys file."}, nil
	}
	if present {
		return http.StatusConflict, pairReply{Error: "This key is in the authorized keys file already."}, nil
	}

	s.used = true
	fp := ssh.FingerprintSHA256(pub)
	check := pairCheckCode(s.token, req.Key)
	if !s.yes {
		ok, why := s.confirm(device, keyType, fp, check, r.remote)
		if !ok {
			s.finish(nil, errors.New(why))
			return http.StatusForbidden, pairReply{Error: "The person at the computer did not accept the key."}, nil
		}
	}

	s.commit.Lock()
	defer s.commit.Unlock()
	select {
	case <-s.done:
		return http.StatusGone, pairReply{Error: "Pairing is stopped. Run tuios pair again."}, nil
	default:
	}

	note, err := s.writePolicy(device)
	if err != nil {
		s.finish(nil, err)
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot save the policy for this device. It did not add the key."}, nil
	}

	command := s.tuiosPath + " stdio-proxy --as " + device
	line := `command="` + command + `",restrict ` + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " tuios-pair:" + device
	if err := hostsetup.AppendAuthorizedKey(s.keysPath, line); err != nil {
		s.finish(nil, errfmt.Errorf("cannot add the key to %s: %w. config.toml has a new [hosts.%s] table. Remove it before you pair this device again", s.keysPath, err, device))
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot save the key."}, nil
	}

	res := &pairResult{Device: device, KeyType: keyType, Fingerprint: fp, Check: check, Allow: s.allow, AuthorizedKeys: s.keysPath, Config: "written", configNote: note}
	s.finish(res, nil)
	return http.StatusOK, pairReply{
		OK:      true,
		Device:  device,
		User:    s.user,
		Command: command,
		HostKey: s.hostKey.line,
		MAC:     pairMAC(s.token, pairProtocol+"-ok\n"+device+"\n"+s.hostKey.line),
		Check:   check,
	}, res
}

func (s *pairServer) sourceRefusal(remote string) string {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return ""
	}
	from := ap.Addr().Unmap()
	if name, ok := s.peers[from]; ok {
		return fmt.Sprintf("a request with the code came from %s, which is the host %s in config.toml. A program there may have read the code. Pairing is stopped, and nothing is changed. Run tuios pair again", from, name)
	}
	if !s.local && pairOwnAddr(from) {
		return fmt.Sprintf("a request with the code came from this machine (%s). A program here may have read the code. Pairing is stopped, and nothing is changed. Run tuios pair again. For a phone in an emulator, or behind a userspace tailscaled, add --accept-local", from)
	}
	return ""
}

func pairOwnAddr(a netip.Addr) bool {
	if a.IsLoopback() || a.IsUnspecified() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, ia := range addrs {
		ipn, ok := ia.(*net.IPNet)
		if !ok {
			continue
		}
		if own, ok := netip.AddrFromSlice(ipn.IP); ok && own.Unmap() == a {
			return true
		}
	}
	return false
}

func (s *pairServer) confirm(device, keyType, fp, check, from string) (bool, string) {
	fmt.Fprintf(s.prompt, "\nA device asks to pair.\n  Name:  %s\n  Check: %s\n  Key:   %s %s\n  From:  %s\n  Allow: %s\nCompare the check code and the key with the phone. Add this key? [y/N] ",
		device, check, keyType, fp, from, strings.Join(s.allow, ", "))
	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(s.stdin).ReadString('\n')
		answer <- strings.ToLower(strings.TrimSpace(line))
	}()
	timer := time.NewTimer(time.Until(s.deadline))
	defer timer.Stop()
	select {
	case a := <-answer:
		if f, ok := s.stdin.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
			fmt.Fprintln(s.prompt, a)
		}
		if a == "y" || a == "yes" {
			return true, ""
		}
		return false, "the key was not accepted. Nothing is changed"
	case <-timer.C:
		fmt.Fprintln(s.prompt)
		return false, "no answer before the time ran out. Nothing is changed"
	case <-s.done:
		fmt.Fprintln(s.prompt)
		return false, "pairing is stopped. Nothing is changed"
	}
}

func (s *pairServer) writePolicy(device string) (string, error) {
	note, err := config.AddLinkPolicyInFile(s.config, device, s.allow)
	if errors.Is(err, config.ErrHostTableExists) {
		return "", errfmt.Errorf("config.toml got a [hosts.%s] table while tuios waited. tuios did not change it and did not add the key. Run tuios pair again with another name", device)
	}
	if err != nil {
		return "", errfmt.Errorf("cannot write [hosts.%s] to %s: %w. tuios did not add the key", device, s.config, err)
	}
	printWriteNote(note)
	hosts, err := config.HostsInFile(s.config)
	if err != nil {
		return "", errfmt.Errorf("config.toml does not read after tuios wrote [hosts.%s]: %w. tuios did not add the key. Correct the file", device, err)
	}
	got := config.LinkPolicyFor(hosts, device)
	if got.Source != "hosts."+device || !sameCapabilities(got.Allow, s.allow) {
		return "", errfmt.Errorf("after the write, the policy of %s comes from %s with allow = %s, not from [hosts.%s] with allow = %s. tuios did not add the key", device, got.Source, strings.Join(got.Allow, ", "), device, strings.Join(s.allow, ", "))
	}
	msg := fmt.Sprintf("config.toml now has [hosts.%s] with allow = %s.", tomlHostKey(device), strings.Join(s.allow, ", "))
	if slices.Contains(s.allow, config.LinkAllowRespond) {
		msg += " " + configWaitsNote
	}
	return msg, nil
}

func sameCapabilities(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

func tomlHostKey(name string) string {
	if strings.Contains(name, ".") {
		return `"` + name + `"`
	}
	return name
}

func pairKeyAccepted(pub ssh.PublicKey) (keyType, refusal string) {
	switch pub.Type() {
	case ssh.KeyAlgoECDSA256:
		return "ECDSA", ""
	case ssh.KeyAlgoED25519:
		return "ED25519", ""
	case ssh.KeyAlgoRSA:
		if ck, ok := pub.(ssh.CryptoPublicKey); ok {
			if rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey); ok && rk.N.BitLen() >= 3072 {
				return "RSA", ""
			}
		}
		return "", "This RSA key is too short. Use 3072 bits or more, or use ECDSA P-256 or Ed25519."
	}
	return "", "This key type is not accepted. Use ECDSA P-256, Ed25519, or RSA with 3072 bits or more."
}

func writePairReply(w io.Writer, status int, reply pairReply) {
	body, _ := jsonx.Marshal(reply)
	body = append(body, '\n')
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		status, http.StatusText(status), len(body))
	_, _ = w.Write(body)
}

func renderQR(text string, colour bool) string {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "The link is too long for a QR code.\n"
	}
	const quiet = 2
	n := code.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := range n {
			top, bottom := dark(x, y), y+1 < n && dark(x, y+1)
			if colour {
				fg, bg := "97", "107"
				if top {
					fg = "30"
				}
				if bottom {
					bg = "40"
				}
				b.WriteString("\x1b[")
				b.WriteString(fg)
				b.WriteString(";")
				b.WriteString(bg)
				b.WriteString("m▀")
				continue
			}
			switch {
			case !top && !bottom:
				b.WriteString("█")
			case !top:
				b.WriteString("▀")
			case !bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if colour {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\n")
	}
	return b.String()
}
