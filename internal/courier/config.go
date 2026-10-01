package courier

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/netutil"
	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// HomeEnv overrides where the courier keeps its files. With it set, the
// config and the key are at its root and the mail is under state/. It is how
// tests and a second identity on one machine stay apart.
const HomeEnv = "TUIOS_COURIER_HOME"

// Release policies.
const (
	// ReleaseHold keeps mail from the person's agents until the person
	// releases it.
	ReleaseHold = "hold"
	// ReleaseAuto lets the agents read it as it arrives.
	ReleaseAuto = "auto"
)

// namePattern is what the person's own name and a peer's name may be: the
// same rule tuios applies to host names.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName reports whether s may name a person.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// Paths are where the courier's files live.
type Paths struct {
	ConfigDir string
	StateDir  string
}

// DefaultPaths are $TUIOS_COURIER_HOME, or the XDG config and state
// directories under tuios/courier.
func DefaultPaths() Paths {
	if home := os.Getenv(HomeEnv); home != "" {
		return Paths{ConfigDir: home, StateDir: filepath.Join(home, "state")}
	}
	return Paths{
		ConfigDir: filepath.Join(xdg.ConfigHome, "tuios", "courier"),
		StateDir:  filepath.Join(xdg.StateHome, "tuios", "courier"),
	}
}

func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "courier.toml") }
func (p Paths) KeyFile() string    { return filepath.Join(p.ConfigDir, "identity.key") }

// Peer is one person this one exchanges mail with.
type Peer struct {
	Name     string
	Identity Identity
	// Release is what happens to a new message from this peer: ReleaseHold
	// or ReleaseAuto.
	Release string
}

// Config is the courier's configuration.
type Config struct {
	Name  string
	Relay string
	// ReplyRelease is what happens to a reply in a thread this person started,
	// from the peer it was started with.
	ReplyRelease string
	Peers        map[string]Peer
}

type fileConfig struct {
	Name         string              `toml:"name"`
	Relay        string              `toml:"relay"`
	ReplyRelease string              `toml:"reply_release,omitempty"`
	Peers        map[string]filePeer `toml:"peers,omitempty"`
}

type filePeer struct {
	Identity string `toml:"identity"`
	Release  string `toml:"release,omitempty"`
}

// LoadConfig reads and checks the config at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no courier config at %s: run tuios-courier init --name NAME --relay URL", path)
	}
	if err != nil {
		return nil, err
	}
	var fc fileConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fc); err != nil {
		return nil, fmt.Errorf("courier config %s: %v", path, err)
	}
	c := &Config{Name: fc.Name, Relay: fc.Relay, ReplyRelease: fc.ReplyRelease, Peers: map[string]Peer{}}
	if c.ReplyRelease == "" {
		c.ReplyRelease = ReleaseAuto
	}
	for name, fp := range fc.Peers {
		id, err := ParseIdentity(fp.Identity)
		if err != nil {
			return nil, fmt.Errorf("courier config %s: peer %q: %v", path, name, err)
		}
		release := fp.Release
		if release == "" {
			release = ReleaseHold
		}
		c.Peers[name] = Peer{Name: name, Identity: id, Release: release}
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("courier config %s: %v", path, err)
	}
	return c, nil
}

func (c *Config) validate() error {
	if !ValidName(c.Name) {
		return fmt.Errorf("name %q may hold only letters, digits, '.', '_' and '-'", clip(c.Name))
	}
	if err := ValidateRelayURL(c.Relay); err != nil {
		return err
	}
	if c.ReplyRelease != ReleaseAuto && c.ReplyRelease != ReleaseHold {
		return fmt.Errorf("reply_release %q is not auto or hold", clip(c.ReplyRelease))
	}
	seen := map[Identity]string{}
	for _, name := range c.PeerNames() {
		p := c.Peers[name]
		if !ValidName(name) {
			return fmt.Errorf("peer name %q may hold only letters, digits, '.', '_' and '-'", clip(name))
		}
		if p.Release != ReleaseAuto && p.Release != ReleaseHold {
			return fmt.Errorf("peer %s: release %q is not auto or hold", name, clip(p.Release))
		}
		if other, dup := seen[p.Identity]; dup {
			return fmt.Errorf("peers %s and %s have the same identity", other, name)
		}
		seen[p.Identity] = name
	}
	return nil
}

// ValidateRelayURL accepts an https URL with a host, or http to a loopback
// address. Mail is sealed either way, but the request signatures and who
// talks to whom are not, so clear text leaves this machine only on purpose:
// through an ingress that terminates TLS in front of the relay.
func ValidateRelayURL(s string) error {
	if s == "" {
		return errors.New("no relay URL: set relay = \"https://…\" or run tuios-courier init --relay URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("relay URL %q: %v", clip(s), err)
	}
	if u.Host == "" {
		return fmt.Errorf("relay URL %q has no host", clip(s))
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if netutil.IsLoopbackHost(host) {
			return nil
		}
		return fmt.Errorf("relay URL %q is http to another machine: use https", clip(s))
	}
	return fmt.Errorf("relay URL %q is not https", clip(s))
}

// PeerNames is every peer name, sorted.
func (c *Config) PeerNames() []string {
	names := make([]string, 0, len(c.Peers))
	for n := range c.Peers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Lookup names the peer with identity id. It is the PeerLookup Open takes.
func (c *Config) Lookup(id Identity) (string, bool) {
	for name, p := range c.Peers {
		if p.Identity.Equal(id) {
			return name, true
		}
	}
	return "", false
}

// AddPeer adds a peer. It never replaces one: a changed key for a name is
// something the person removes and adds on purpose.
func (c *Config) AddPeer(name string, id Identity, release string) error {
	if !ValidName(name) {
		return fmt.Errorf("peer name %q may hold only letters, digits, '.', '_' and '-'", clip(name))
	}
	if release != ReleaseAuto && release != ReleaseHold {
		return fmt.Errorf("release %q is not auto or hold", clip(release))
	}
	if _, ok := c.Peers[name]; ok {
		return fmt.Errorf("peer %s already exists: remove it first to change its identity", name)
	}
	if other, ok := c.Lookup(id); ok {
		return fmt.Errorf("that identity is already peer %s", other)
	}
	if c.Peers == nil {
		c.Peers = map[string]Peer{}
	}
	c.Peers[name] = Peer{Name: name, Identity: id, Release: release}
	return nil
}

// RemovePeer removes a peer by name.
func (c *Config) RemovePeer(name string) error {
	if _, ok := c.Peers[name]; !ok {
		return fmt.Errorf("no peer named %s", name)
	}
	delete(c.Peers, name)
	return nil
}

// Save writes the config to path, readable by its owner only.
func (c *Config) Save(path string) error {
	fc := fileConfig{Name: c.Name, Relay: c.Relay, ReplyRelease: c.ReplyRelease}
	if len(c.Peers) > 0 {
		fc.Peers = map[string]filePeer{}
		for name, p := range c.Peers {
			fc.Peers[name] = filePeer{Identity: p.Identity.String(), Release: p.Release}
		}
	}
	data, err := toml.Marshal(fc)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

// writeFileAtomic replaces path with data through a temporary file in the same
// directory, so a reader sees the old file or the new one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, mode)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}
