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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"rsc.io/qr"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios pair: a phone adds this machine by scanning a QR code.
//
// The code holds a tuios://pair URL with the ssh addresses, the ssh host key
// fingerprint and a one-time token. The phone posts its public key to a short
// lived HTTP listener here, with an HMAC of the key made with the token. The
// listener never sees the token itself, so a machine on the path that did not
// see the code cannot put its own key in place of the phone's. After the
// person confirms, the key goes into authorized_keys behind a forced command
// that can run nothing but `tuios stdio-proxy --as DEVICE`, so the key opens a
// link under a pinned name and the [hosts.DEVICE] link policy applies to it.
//
// The reply carries the host key line and an HMAC over it, so the phone can
// pin the key it will see on its first ssh connection without trusting the
// network for it.
//
// Nothing else changes: no daemon is asked to do anything, and the only files
// written are one new [hosts.DEVICE] table in the config and then
// authorized_keys. The table goes first, and the key is written only once the
// table is in the file and reads back as the policy of DEVICE: a key whose
// name has no table of its own would get [hosts."*"] or the built-in default,
// which can start processes and type into panes. A running daemon picks the
// new table up from its watch on config.toml, as a change that narrows.

const (
	// pairProtocol is the v= value of the URL and the prefix of both MAC
	// inputs. A change to either message bumps it.
	pairProtocol = "tuios-pair-v1"
	// pairPath is the one path the listener answers.
	pairPath = "/v1/pair"
	// pairBodyLimit caps a request body. A P-256 or Ed25519 key line is under
	// 200 bytes and an RSA 4096 one under 800.
	pairBodyLimit = 8 << 10
	// pairFailureLimit is how many requests that fail before the MAC check,
	// or fail it, the listener takes before it stops for good. The token is
	// 128 bits, so this is not about guessing: a phone with the code makes
	// none of these, so three of them mean someone else is trying.
	pairFailureLimit = 3
	// pairFailurePause slows each failed request, so the limit above cannot
	// be spent in a burst.
	pairFailurePause = 200 * time.Millisecond
	// pairHeaderLimit caps the request line and headers together.
	pairHeaderLimit = 4 << 10
	// pairConnDeadline is how long one connection may take, from accept to
	// the reply.
	pairConnDeadline = 10 * time.Second
	// pairConnLimit caps the connections open at once. More are closed at
	// accept.
	pairConnLimit = 16
	// pairMaxTimeout bounds --timeout. A pairing code is meant to be scanned
	// now, not left on a screen.
	pairMaxTimeout = time.Hour
	// pairLANLimit is how many LAN addresses go into the code, to keep it
	// small enough to scan.
	pairLANLimit = 3
)

// pairOptions are the flags of tuios pair.
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

// pairDefaultAllow is what a paired device may do when --allow is not given:
// read listings and send and read mail. Starting processes or typing into
// panes from a phone is something the person turns on by name.
var pairDefaultAllow = []string{config.LinkAllowList, config.LinkAllowMail}

func newPairCommand() *cobra.Command {
	var o pairOptions
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Show a QR code that adds this machine to a phone",
		Long: `Show a QR code that adds this machine to a phone.

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
	f.BoolVar(&o.jsonOut, "json", false, "Print the link and the result as JSON lines")
	f.StringSliceVar(&o.advertiseSSH, "advertise-ssh", nil, "The ssh addresses for the phone, as HOST:PORT. They replace the addresses tuios finds")
	f.StringSliceVar(&o.advertisePair, "advertise-pair", nil, "The pairing addresses for the phone, as HOST:PORT. They replace the addresses tuios finds")
	f.StringVar(&o.hostKey, "host-key", "", "The ssh host public key to give the phone (default the first of /etc/ssh/ssh_host_{ed25519,ecdsa,rsa}_key.pub)")
	f.IntVar(&o.sshPort, "ssh-port", 22, "Port of the ssh server, for the addresses tuios finds")
	f.StringVar(&o.command, "command", "", "Path of tuios in the forced command (default the tuios on PATH when it is this tuios)")
	return cmd
}

// pairHostKey is the ssh host key the phone is told to expect.
type pairHostKey struct {
	line        string // "TYPE BASE64", without a comment
	fingerprint string // SHA256:..., as ssh-keygen -lf prints it
}

// pairRequest is the body the phone posts.
type pairRequest struct {
	Device string `json:"device"`
	Key    string `json:"key"`
	MAC    string `json:"mac"`
}

// pairReply is what the listener answers.
type pairReply struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Device  string `json:"device,omitempty"`
	User    string `json:"user,omitempty"`
	Command string `json:"command,omitempty"`
	HostKey string `json:"host_key,omitempty"`
	MAC     string `json:"mac,omitempty"`
	// Check is the check code of the request, which the phone shows while
	// the person compares it with the one on this screen. See pairCheckCode.
	Check string `json:"check,omitempty"`
}

// pairResult is what a successful pairing did, for the final report.
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

// pairAddr is one address the code names: what the phone dials, and what
// this side binds for it.
type pairAddr struct {
	advertise string // a host name or IP, without a port
	bind      string // an IP, without a port
	public    bool   // bind is a public address
}

func runPair(ctx context.Context, o pairOptions, stdout, stderr io.Writer, stdin io.Reader) error {
	if o.timeout <= 0 || o.timeout > pairMaxTimeout {
		return fmt.Errorf("--timeout must be more than 0 and at most %s", pairMaxTimeout)
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
		return fmt.Errorf("cannot find config.toml, where the policy of the device goes: %w", err)
	}
	hosts, err := pairHosts(configPath)
	if err != nil {
		return err
	}
	keysPath := o.authorizedKeys
	if keysPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot find your home folder: %w. Use --authorized-keys", err)
		}
		keysPath = filepath.Join(home, ".ssh", "authorized_keys")
	}
	if keysPath, err = filepath.Abs(keysPath); err != nil {
		return err
	}
	if err := checkAuthorizedKeysModes(keysPath, stderr); err != nil {
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
		return fmt.Errorf("cannot find your user name: %w", err)
	}
	machine := o.machine
	if machine != "" {
		if err := session.ValidLinkPeerName(machine); err != nil {
			return fmt.Errorf("--machine: %w", err)
		}
	} else {
		machine, _ = os.Hostname()
		machine, _, _ = strings.Cut(machine, ".")
	}

	// tailscale is asked once, and only when something needs its answer:
	// the addresses in the code, or the tailnet addresses of the hosts in
	// [hosts], which a request must not come from.
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
			return fmt.Errorf("--name %s: %s. Give the device another name", o.name, why)
		}
	}
	peers := pairPeerAddrs(ctx, hosts, tailnet)

	// The addresses tuios finds are needed for any list the flags leave to
	// it: the ssh addresses, the listeners, and what a listener on every
	// interface is advertised as.
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
			return fmt.Errorf("%q is not an address. Write HOST:PORT", a)
		}
	}

	if listensEverywhere(o.listen) {
		if !o.listenAll {
			return fmt.Errorf("--listen %s listens on every interface. Give one address of this machine, or add --listen-all", o.listen)
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
		return fmt.Errorf("cannot make a pairing token: %w", err)
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

// pairDeviceNameOK applies the rules of a link peer name and of a host
// table name: the device gets a [hosts.DEVICE] table.
func pairDeviceNameOK(name string) error {
	if err := session.ValidLinkPeerName(name); err != nil {
		return err
	}
	return federation.ValidHostName(name)
}

// pairHosts reads the [hosts] table of the config. A config that is not there
// has none. A config that does not parse stops the pairing: tuios could not
// tell whether the device's table reads back, and the daemon may not read it.
func pairHosts(path string) (map[string]config.HostConfig, error) {
	hosts, err := config.HostsInFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]config.HostConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s, where the policy of the device goes: %w. Correct the file and run tuios pair again", path, err)
	}
	return hosts, nil
}

// pairNameInUse says why a device name may not be used here, or returns "".
// A name is in use when it names a [hosts] table, a machine that a key in the
// authorized keys file opens a link as, or this machine. Case does not
// matter, because the link policy matches names without case.
//
// A [hosts] table: the device would get that machine's policy, and the table
// would be written over or shared. A pinned key: two keys would open links
// under one name, and the person could not tell their links apart or remove
// one. This machine: links and mail from the phone would look like they came
// from here.
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

// pairPinnedNamePattern finds the name in a forced command that pins one.
var pairPinnedNamePattern = regexp.MustCompile(`stdio-proxy\s+--as[ =]([A-Za-z0-9._-]+)`)

// pairPinnedNames is every name that a forced command in the authorized keys
// file pins with stdio-proxy --as.
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

// pairPeerAddrs maps each address of a machine in [hosts] to its name. A
// pairing request from one of them is refused, because a linked machine can
// read this machine's panes, the code among them.
//
// A host is known by its addr: an IP, or a name that resolves through DNS
// or that names a machine on the tailnet. A host added with --tailnet has
// its tailnet name as the table name too, so that is matched as well. A host
// that ssh reaches through an alias in ~/.ssh/config, with no DNS name, is
// not found. tuios does not run ssh to resolve it.
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

// pairAddrHost is the host part of a [hosts] addr: without the user, the port
// and the brackets of an IPv6 address.
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

// pairAllowList checks --allow against the link capabilities.
func pairAllowList(in []string) ([]string, error) {
	var out []string
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !slices.Contains(config.LinkCapabilities, c) {
			return nil, fmt.Errorf("--allow: %q is not a capability. Use %s", c, strings.Join(config.LinkCapabilities, ", "))
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// loadPairHostKey reads the host key to advertise: the file given, or the
// first host key in /etc/ssh.
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
			return pairHostKey{}, fmt.Errorf("cannot read the host key: %w", err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return pairHostKey{}, fmt.Errorf("%s is not an ssh public key: %w", p, err)
		}
		return pairHostKey{
			line:        strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
			fingerprint: ssh.FingerprintSHA256(pub),
		}, nil
	}
	return pairHostKey{}, errors.New("tuios found no ssh host key in /etc/ssh. Use --host-key")
}

// pairTuiosPath is the tuios path for the forced command, written so the
// shell sshd runs it with reads it as one word.
//
// The default is the tuios on PATH, as PATH names it, when it is this
// binary. os.Executable resolves links, so it names the file a package
// manager replaces on an upgrade: /nix/store/HASH-tuios/bin/tuios, or
// /opt/homebrew/Cellar/tuios/VERSION/bin/tuios. A forced command with that
// path stops working after the next upgrade, and the phone can no longer
// link. The PATH entry (~/.nix-profile/bin/tuios, /opt/homebrew/bin/tuios)
// is the link the package manager keeps current. A tuios on PATH that is
// another binary is not used, because the phone would then run a version
// nobody chose. tuios then uses this binary and says why.
func pairTuiosPath(given string, stderr io.Writer) (string, error) {
	p := given
	if p == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("cannot find the path of tuios: %w. Use --command", err)
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
		return "", fmt.Errorf("the path %q holds a quote, a backslash or a control character. Use --command with another path", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("the path %q holds a control character. Use --command with another path", p)
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

// pairSameFile reports whether two paths reach one file, after links.
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

// discoverPairAddrs finds the addresses to put in the code: this machine's
// MagicDNS name when tailscale answers, then LAN IPv4 addresses. Each LAN
// address says whether it is public, which the listener does not bind
// without --listen-all.
func discoverPairAddrs(machines []federation.TailnetMachine) []pairAddr {
	var out []pairAddr
	for _, m := range machines {
		if !m.Self || m.IP == "" {
			continue
		}
		adv := m.DNSName
		if adv == "" {
			adv = m.IP
		}
		out = append(out, pairAddr{advertise: adv, bind: m.IP})
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	ifaces, _ := net.Interfaces()
	lan := 0
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || pairSkipInterface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) || lan >= pairLANLimit {
				continue
			}
			out = append(out, pairAddr{advertise: ip.String(), bind: ip.String(), public: pairPublicIP(ip)})
			lan++
		}
	}
	return out
}

// pairPublicIP reports whether ip can be reached from the internet: a global
// unicast address that is not private (RFC 1918, fc00::/7) and not in the
// shared range tailnets use (100.64.0.0/10).
func pairPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return !cgnat.Contains(ip)
}

// pairSkipInterface leaves out the interfaces of containers, VMs and VPNs: a
// phone cannot reach them, and each address makes the code bigger.
func pairSkipInterface(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "podman", "cni", "flannel", "tailscale", "utun", "zt", "vmnet", "vboxnet", "lxc", "lxd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// listensEverywhere reports whether --listen names the unspecified address,
// such as 0.0.0.0:7420 or :7420.
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

// openPairListeners binds the pairing listener. With --listen it binds that
// one address. Otherwise it binds each found address, on one port when it can.
// Every interface and a public address need --listen-all: the listener is
// plain HTTP, and the internet has no reason to reach it.
func openPairListeners(listen string, listenAll bool, found []pairAddr, discover func() []pairAddr, stderr io.Writer) ([]net.Listener, []string, error) {
	if listen != "" {
		if host, _, err := net.SplitHostPort(listen); err == nil && !listenAll && pairPublicIP(net.ParseIP(host)) {
			return nil, nil, fmt.Errorf("--listen %s is a public address. Give a private or tailnet address of this machine, or add --listen-all", listen)
		}
		l, err := net.Listen("tcp", listen)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot listen on %s: %w", listen, err)
		}
		host, port, _ := net.SplitHostPort(l.Addr().String())
		var adv []string
		ip := net.ParseIP(host)
		// Check the address that was bound, not the text. A host such as
		// "0" or a name that resolves to 0.0.0.0 binds every interface too,
		// and a name can resolve to a public address.
		if !listenAll && pairPublicIP(ip) {
			_ = l.Close()
			return nil, nil, fmt.Errorf("--listen %s is a public address. Give a private or tailnet address of this machine, or add --listen-all", listen)
		}
		if ip != nil && ip.IsUnspecified() {
			if !listenAll {
				_ = l.Close()
				return nil, nil, fmt.Errorf("--listen %s listens on every interface. Give one address of this machine, or add --listen-all", listen)
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

// pairURL is the link the QR code holds.
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

// pairMAC is base64url(HMAC-SHA256(token, msg)), without padding.
func pairMAC(token []byte, msg string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// pairCheckCode is the six digit check code of a request. Both screens show
// it: this one in the question, the phone while it waits for the answer.
// A person who photographed the code and sent their own key gets another
// code than the phone shows.
//
// It is HMAC-SHA256(token, "tuios-pair-v1-check\n" + key), where key is the
// key field of the request byte for byte. The first four bytes of the MAC,
// read as a big-endian unsigned 32-bit integer, modulo 1000000, written as
// six decimal digits with leading zeros.
func pairCheckCode(token []byte, key string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(pairProtocol + "-check\n" + key))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(m.Sum(nil)[:4])%1000000)
}

// pairServer is the listener's state. It answers one request at a time.
type pairServer struct {
	token     []byte
	deadline  time.Time
	name      string
	allow     []string
	config    string                // the config.toml the table goes to
	ownNames  []string              // the names of this machine
	peers     map[netip.Addr]string // the addresses of [hosts], by name
	local     bool                  // --accept-local
	yes       bool
	keysPath  string
	hostKey   pairHostKey
	user      string
	tuiosPath string
	stdin     io.Reader
	prompt    io.Writer

	busy sync.Mutex
	// commit orders the write of the key against a stop. A stop (Ctrl+C or
	// the time) that comes first means nothing is written. A write that
	// starts first ends, and the stop then finds the pairing finished.
	commit   sync.Mutex
	failures int
	used     bool
	result   *pairResult
	err      error
	done     chan struct{}
	doneOnce sync.Once
}

// stop ends the pairing from outside: Ctrl+C or the time. It waits for a
// key write in progress, so the result reported is what happened.
func (s *pairServer) stop(err error) {
	s.commit.Lock()
	defer s.commit.Unlock()
	s.finish(nil, err)
}

// finish ends the pairing with a result or an error.
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
	// Wait for the reply in flight, so the phone gets its answer. Each
	// connection has a deadline, so this ends.
	waited := make(chan struct{})
	go func() { conns.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(pairConnDeadline):
	}
	return s.result, s.err
}

// serveConn answers the one request on a connection, then closes it.
func (s *pairServer) serveConn(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(pairConnDeadline))
	req := readPairHTTP(bufio.NewReaderSize(c, 1024), c)
	if req.closed {
		return
	}
	req.remote = c.RemoteAddr().String()
	status, reply, _ := s.answer(req)
	// The question to the person can take minutes, far past the deadline set
	// at accept. Give the reply its own time, or the key is added and the
	// phone never learns the host key to pin.
	_ = c.SetWriteDeadline(time.Now().Add(pairConnDeadline))
	writePairReply(c, status, reply)
}

// answer handles one request that was read, one at a time.
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

// pairHTTP is one HTTP/1.1 request as readPairHTTP read it.
type pairHTTP struct {
	method, path, remote string
	body                 []byte
	// bad is the status for a request that could not be read, or 0.
	bad int
	// closed is a connection that the client closed before it sent a byte.
	// It is not a request, so it gets no answer and is not counted.
	closed bool
}

// readPairHTTP reads one request: the request line, the headers and a body
// of Content-Length bytes.
//
// The listener does not use net/http's server, which would add about 0.5 MB
// to the binary for one request (the --pprof server does the same; see
// pprof_server.go). What it accepts is narrower, and what a phone sends fits:
//   - The body needs a Content-Length. Chunked bodies are refused with 411.
//   - One request per connection. The reply closes it.
//   - Expect: 100-continue gets its interim reply, so curl works.
//   - The request line and headers together are at most pairHeaderLimit
//     bytes, and the whole request must arrive within pairConnDeadline.
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
		// A client that opens a connection and closes it unused, as one that
		// races its connections to each address does, did not try anything.
		// One that holds it open without a byte until the deadline did.
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

// fail counts a well-formed request that did not pass the MAC check, and
// slows the next. Only those count: a request of another shape cannot pair
// whatever it holds, and a browser, a scanner or a probe that sends one must
// not end the pairing.
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

// refuse answers a request that is not a pairing request. It is not counted.
func refuse(status int, msg string) (int, pairReply, *pairResult) {
	return status, pairReply{Error: msg}, nil
}

// handle checks a request and, when it is good, adds the key.
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
	if err := json.Unmarshal(r.body, &req); err != nil || req.Device == "" || req.Key == "" || req.MAC == "" {
		return refuse(http.StatusBadRequest, "The request must be JSON with device, key and mac.")
	}
	want := pairMAC(s.token, pairProtocol+"\n"+req.Device+"\n"+req.Key)
	if !hmac.Equal([]byte(want), []byte(req.MAC)) {
		return s.fail(http.StatusForbidden, "The code does not match. Scan the QR code again.")
	}

	// From here the request comes from someone who has the code. When that
	// is this machine or a machine it links with, a program there read the
	// code off the screen or out of a pane, and it is not the phone. The
	// code is spent, so the program cannot try again.
	if why := s.sourceRefusal(r.remote); why != "" {
		s.used = true
		s.finish(nil, errors.New(why))
		return http.StatusForbidden, pairReply{Error: "This computer does not accept a pairing request from itself or from a machine it links with. Pairing is stopped."}, nil
	}

	// A mistake from here is reported and the code stays good, so the phone
	// can try again.
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
	// ParseAuthorizedKey skips lines it cannot read, so "junk\nKEY" would
	// pass as one key. A key is one line, with a line end at most.
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(req.Key))
	if err != nil || strings.TrimSpace(string(rest)) != "" || strings.ContainsAny(strings.TrimRight(req.Key, "\r\n"), "\r\n") {
		return http.StatusBadRequest, pairReply{Error: "The key is not one OpenSSH public key line."}, nil
	}
	keyType, refusal := pairKeyAccepted(pub)
	if refusal != "" {
		return http.StatusBadRequest, pairReply{Error: refusal}, nil
	}
	present, err := authorizedKeysHas(s.keysPath, pub)
	if err != nil {
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot read its authorized keys file."}, nil
	}
	if present {
		return http.StatusConflict, pairReply{Error: "This key is in the authorized keys file already."}, nil
	}

	// The code is spent now, whatever the person answers.
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

	// The person may answer after Ctrl+C or after the time ran out. tuios
	// has said that pairing is stopped by then, so nothing may be written.
	s.commit.Lock()
	defer s.commit.Unlock()
	select {
	case <-s.done:
		return http.StatusGone, pairReply{Error: "Pairing is stopped. Run tuios pair again."}, nil
	default:
	}

	// The policy first. A key whose name has no table would get
	// [hosts."*"] or the built-in default, which can start processes and
	// type into panes. So the key is written only when the table is in the
	// file and reads back as the policy of the device.
	note, err := s.writePolicy(device)
	if err != nil {
		s.finish(nil, err)
		return http.StatusInternalServerError, pairReply{Error: "The computer cannot save the policy for this device. It did not add the key."}, nil
	}

	command := s.tuiosPath + " stdio-proxy --as " + device
	line := `command="` + command + `",restrict ` + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " tuios-pair:" + device
	if err := appendAuthorizedKey(s.keysPath, line); err != nil {
		s.finish(nil, fmt.Errorf("cannot add the key to %s: %w. config.toml has a new [hosts.%s] table. Remove it before you pair this device again", s.keysPath, err, device))
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

// sourceRefusal says why a request with a good MAC may not come from where
// it came from, or returns "".
//
// This machine: any address of its own interfaces, loopback included. A
// program here that can read the screen, or a pane through tuios, has the
// code. A phone in an emulator, or one that reaches this machine through a
// userspace tailscaled, which hands every tailnet connection over from
// 127.0.0.1, needs --accept-local.
//
// A machine it links with: an address of a [hosts] entry (see
// pairPeerAddrs). A machine this one links with can read its panes over the
// link, so a program there can have the code too. --accept-local does not
// change this.
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

// pairOwnAddr reports whether a is an address of this machine.
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

// confirm asks the person to accept the key. It returns false and the reason
// when they do not, or when the time runs out first.
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
			// A terminal echoes the answer and its newline. Input from a
			// pipe does not, so the next line would join the question.
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

// writePolicy writes the new [hosts.DEVICE] table and reads the config back.
// It returns a sentence for the person, or an error when the table is not
// in the file as the policy of the device. Then the key must not be added.
func (s *pairServer) writePolicy(device string) (string, error) {
	note, err := config.AddLinkPolicyInFile(s.config, device, s.allow)
	if errors.Is(err, config.ErrHostTableExists) {
		return "", fmt.Errorf("config.toml got a [hosts.%s] table while tuios waited. tuios did not change it and did not add the key. Run tuios pair again with another name", device)
	}
	if err != nil {
		return "", fmt.Errorf("cannot write [hosts.%s] to %s: %w. tuios did not add the key", device, s.config, err)
	}
	printWriteNote(note)
	hosts, err := config.HostsInFile(s.config)
	if err != nil {
		return "", fmt.Errorf("config.toml does not read after tuios wrote [hosts.%s]: %w. tuios did not add the key. Correct the file", device, err)
	}
	got := config.LinkPolicyFor(hosts, device)
	if got.Source != "hosts."+device || !sameCapabilities(got.Allow, s.allow) {
		return "", fmt.Errorf("after the write, the policy of %s comes from %s with allow = %s, not from [hosts.%s] with allow = %s. tuios did not add the key", device, got.Source, strings.Join(got.Allow, ", "), device, strings.Join(s.allow, ", "))
	}
	msg := fmt.Sprintf("config.toml now has [hosts.%s] with allow = %s.", tomlHostKey(device), strings.Join(s.allow, ", "))
	if slices.Contains(s.allow, config.LinkAllowRespond) {
		msg += " " + configWaitsNote
	}
	return msg, nil
}

// sameCapabilities reports whether two allow lists hold the same set.
func sameCapabilities(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// tomlHostKey is the name as a [hosts] key is written: quoted when it holds a
// dot, so [hosts."a.b"] does not read as a nested table.
func tomlHostKey(name string) string {
	if strings.Contains(name, ".") {
		return `"` + name + `"`
	}
	return name
}

// pairKeyAccepted refuses key types other than ECDSA P-256, Ed25519 and RSA
// of 3072 bits or more. It returns a short name for the type, or the reason
// for the phone when it refuses.
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

// authorizedKeysHas reports whether the file holds pub on any line, whatever
// the options and comment of that line. A missing file holds nothing.
func authorizedKeysHas(path string, pub ssh.PublicKey) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the file the person names
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := pub.Marshal()
	for len(data) > 0 {
		k, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			// The parser stops at the first line it cannot read. Skip that
			// line and go on.
			i := strings.IndexByte(string(data), '\n')
			if i < 0 {
				break
			}
			data = data[i+1:]
			continue
		}
		if string(k.Marshal()) == string(want) {
			return true, nil
		}
		data = rest
	}
	return false, nil
}

// checkAuthorizedKeysModes checks the authorized keys file the way sshd's
// StrictModes does, so the pairing does not end with a key that cannot log
// in. sshd refuses the file when it, its folder, or any folder above it up to
// the home folder is writable by others or owned by another user. A file
// outside the home folder is checked with its folder only, as sshd cannot
// be asked which root it walks to.
//
// Writable by anyone, or owned by another user, is refused: anyone could
// have put a key there already. Writable by the group is a warning, because
// Debian's sshd accepts a group that holds only the user.
func checkAuthorizedKeysModes(path string, stderr io.Writer) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	check := []string{path, filepath.Dir(path)}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if rel, err := filepath.Rel(home, filepath.Dir(path)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			for dir := filepath.Dir(filepath.Dir(path)); ; dir = filepath.Dir(dir) {
				check = append(check, dir)
				if dir == home || dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}
	uid := os.Getuid()
	for _, p := range check {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		mode := fi.Mode().Perm()
		if mode&0o002 != 0 {
			return fmt.Errorf("anyone can write to %s (mode %04o). sshd does not use keys under it. Run chmod o-w %s", p, mode, p)
		}
		if owner, ok := fileOwner(fi); ok && owner != 0 && int(owner) != uid {
			return fmt.Errorf("another user owns %s. sshd does not use keys under it. Make it yours or use another --authorized-keys file", p)
		}
		if mode&0o020 != 0 {
			fmt.Fprintf(stderr, "Warning: the group can write to %s (mode %04o). sshd does not use keys under it unless the group holds only you. Run chmod g-w %s\n", p, mode, p)
		}
	}
	return nil
}

// appendAuthorizedKey adds one line to the file. It makes the folder (0700)
// and the file (0600) when they are missing, and leaves the modes of ones
// that exist alone.
func appendAuthorizedKey(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	prefix := ""
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 && data[len(data)-1] != '\n' { //nolint:gosec // the file the person names
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // the file the person names
	if err != nil {
		return err
	}
	if _, err := f.WriteString(prefix + line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writePairReply writes the reply and its status line. The connection
// closes after it.
func writePairReply(w io.Writer, status int, reply pairReply) {
	body, _ := json.Marshal(reply)
	body = append(body, '\n')
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		status, http.StatusText(status), len(body))
	_, _ = w.Write(body)
}

// renderQR draws the code with half blocks, two modules a character. With
// colour it paints black on white whatever the terminal's colours are.
// Without, it draws the light modules as blocks, for a dark terminal.
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
		for x := 0; x < n; x++ {
			top, bottom := dark(x, y), y+1 < n && dark(x, y+1)
			if colour {
				fg, bg := "97", "107"
				if top {
					fg = "30"
				}
				if bottom {
					bg = "40"
				}
				b.WriteString("\x1b[" + fg + ";" + bg + "m▀")
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
