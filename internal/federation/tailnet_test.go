package federation

import (
	"net/netip"
	"testing"

	"go4.org/mem"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

// A tailnet, built by hand. The status is what the local API returns, so a
// test that builds one exercises the same code the real call reaches.
type fakePeer struct {
	host    string
	dns     string
	ip      string
	os      string
	online  bool
	sharee  bool
	tags    []string
	userID  tailcfg.UserID
	isSelf  bool
	noAddrs bool
}

func fakeStatus(peers ...fakePeer) *ipnstate.Status {
	st := &ipnstate.Status{
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{},
		User: map[tailcfg.UserID]tailcfg.UserProfile{
			1: {LoginName: "someone@example.com"},
			2: {LoginName: "other@example.com"},
		},
	}
	for i, p := range peers {
		ps := &ipnstate.PeerStatus{
			HostName:   p.host,
			DNSName:    p.dns,
			OS:         p.os,
			Online:     p.online,
			ShareeNode: p.sharee,
			UserID:     p.userID,
		}
		if !p.noAddrs && p.ip != "" {
			ps.TailscaleIPs = []netip.Addr{netip.MustParseAddr(p.ip)}
		}
		if p.tags != nil {
			tags := views.SliceOf(p.tags)
			ps.Tags = &tags
		}
		if p.isSelf {
			st.Self = ps
			continue
		}
		// Node keys only have to be distinct, so they are made from the index.
		var raw [32]byte
		raw[0] = byte(i + 1)
		st.Peer[key.NodePublicFromRaw32(mem.B(raw[:]))] = ps
	}
	return st
}

func machineNamed(machines []TailnetMachine, name string) (TailnetMachine, bool) {
	for _, m := range machines {
		if m.Name == name {
			return m, true
		}
	}
	return TailnetMachine{}, false
}

// TestTheNameComesFromTheDNSLabelAndNotTheHostname.
//
// A machine reports whatever it calls itself. A phone reports "localhost" and
// a tablet reports its model name with spaces in it, and neither is a name
// anybody could type or that resolves to anything. The DNS label is the name
// the tailnet knows the machine by and the name `tailscale status` prints.
//
// Negative control: taking HostName here gives "localhost", which is both
// wrong and the same for every phone on the tailnet.
func TestTheNameComesFromTheDNSLabelAndNotTheHostname(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "localhost", dns: "my-phone.example.ts.net.", os: "iOS", online: true, userID: 1},
		fakePeer{host: "OnePlus Pad 3", dns: "oneplus-pad-3.example.ts.net.", os: "android", online: true, userID: 1},
	)
	machines := tailnetMachines(st, DefaultTailnetOptions())

	if _, ok := machineNamed(machines, "my-phone"); !ok {
		t.Errorf("the phone is not called by its tailnet name: %+v", machines)
	}
	if _, ok := machineNamed(machines, "oneplus-pad-3"); !ok {
		t.Errorf("the tablet is not called by its tailnet name: %+v", machines)
	}
}

// TestTheTrailingDotIsGone. A MagicDNS name arrives fully qualified with the
// root label on the end. It resolves either way, but nobody types the dot and
// a host address carrying one reads like a mistake.
func TestTheTrailingDotIsGone(t *testing.T) {
	st := fakeStatus(fakePeer{host: "build", dns: "build.example.ts.net.", os: "linux", online: true, userID: 1})
	m, ok := machineNamed(tailnetMachines(st, DefaultTailnetOptions()), "build")
	if !ok {
		t.Fatal("ASSERTION: the machine is not in the list, so there is no address to check")
	}
	if m.DNSName != "build.example.ts.net" || m.Addr != "build.example.ts.net" {
		t.Errorf("the address is %q, want no trailing dot", m.Addr)
	}
}

// TestAMachineThatCannotRunTuiosIsNotOfferedButIsStillListed.
//
// The default filter leaves phones and tablets out, because a host entry
// pointing at one would be a link that can never come up. It is still on the
// list with the reason, so somebody looking for a machine they can see in
// `tailscale status` finds it here rather than wondering where it went.
func TestAMachineThatCannotRunTuiosIsNotOfferedButIsStillListed(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "phone", dns: "phone.example.ts.net.", os: "iOS", online: true, userID: 1},
		fakePeer{host: "build", dns: "build.example.ts.net.", os: "linux", online: true, userID: 1},
	)
	machines := tailnetMachines(st, DefaultTailnetOptions())

	phone, ok := machineNamed(machines, "phone")
	if !ok {
		t.Fatal("the phone is missing from the listing entirely")
	}
	if phone.Offered {
		t.Error("a phone is offered as a machine to run a daemon on")
	}
	if phone.Skipped == "" {
		t.Error("the phone is not offered and the row does not say why")
	}
	if build, _ := machineNamed(machines, "build"); !build.Offered {
		t.Error("a linux machine that is up is not offered")
	}
}

// TestEveryFilterSaysWhyInWordsAPersonCanRead. Each of the four defaults that
// can leave a machine out has to give a reason, because the reason is the only
// thing on the row that tells somebody whether to change a setting or fix the
// machine.
func TestEveryFilterSaysWhyInWordsAPersonCanRead(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "self", dns: "self.example.ts.net.", os: "macOS", isSelf: true, userID: 1},
		fakePeer{host: "asleep", dns: "asleep.example.ts.net.", os: "linux", online: false, userID: 1},
		fakePeer{host: "phone", dns: "phone.example.ts.net.", os: "iOS", online: true, userID: 1},
		fakePeer{host: "theirs", dns: "theirs.example.ts.net.", os: "linux", online: true, sharee: true, userID: 2},
	)
	machines := tailnetMachines(st, DefaultTailnetOptions())

	for _, name := range []string{"self", "asleep", "phone", "theirs"} {
		m, ok := machineNamed(machines, name)
		if !ok {
			t.Errorf("%s is missing from the listing", name)
			continue
		}
		if m.Offered {
			t.Errorf("%s is offered and should not be", name)
		}
		if m.Skipped == "" {
			t.Errorf("%s was left out with no reason on the row", name)
		}
	}
}

// TestTheFiltersCanBeTurnedOff. Every default is a default and not a rule: a
// person who wants their own machine, an offline one, a phone or a shared node
// in the list can have them.
func TestTheFiltersCanBeTurnedOff(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "self", dns: "self.example.ts.net.", os: "macOS", isSelf: true, userID: 1},
		fakePeer{host: "asleep", dns: "asleep.example.ts.net.", os: "linux", online: false, userID: 1},
		fakePeer{host: "phone", dns: "phone.example.ts.net.", os: "iOS", online: true, userID: 1},
		fakePeer{host: "theirs", dns: "theirs.example.ts.net.", os: "linux", online: true, sharee: true, userID: 2},
	)
	opt := DefaultTailnetOptions()
	opt.Self, opt.Offline, opt.Shared = true, true, true
	opt.OS = nil

	for _, m := range tailnetMachines(st, opt) {
		if !m.Offered {
			t.Errorf("%s is still left out with everything turned on: %s", m.Name, m.Skipped)
		}
	}
}

// TestExcludeWinsOverInclude. Two patterns that both match is the case a
// person writes when they mean "all of these except that one", and a rule that
// resolved it the other way would silently keep the machine they named.
func TestExcludeWinsOverInclude(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "build-1", dns: "build-1.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "build-2", dns: "build-2.example.ts.net.", os: "linux", online: true, userID: 1},
	)
	opt := DefaultTailnetOptions()
	opt.Include = []string{"build-*"}
	opt.Exclude = []string{"build-2"}

	machines := tailnetMachines(st, opt)
	if m, _ := machineNamed(machines, "build-1"); !m.Offered {
		t.Error("an included machine is not offered")
	}
	if m, _ := machineNamed(machines, "build-2"); m.Offered {
		t.Error("a machine matched by both include and exclude was offered")
	}
}

// TestAPatternThatDoesNotCompileMatchesNothing. A filter with a typo in it
// must not quietly widen what it matches: an exclude that matched everything
// would empty the list, and an include that did would let everything through.
func TestAPatternThatDoesNotCompileMatchesNothing(t *testing.T) {
	st := fakeStatus(fakePeer{host: "build", dns: "build.example.ts.net.", os: "linux", online: true, userID: 1})
	opt := DefaultTailnetOptions()
	opt.Exclude = []string{"[unclosed"}

	if m, _ := machineNamed(tailnetMachines(st, opt), "build"); !m.Offered {
		t.Error("a broken exclude pattern dropped a machine")
	}
}

// TestTheLoginIsPutInFrontOfTheAddress, since the address goes to ssh and most
// tailnets are reached as a user who is not the one you are sitting at.
func TestTheLoginIsPutInFrontOfTheAddress(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "build", dns: "build.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "other", dns: "other.example.ts.net.", os: "linux", online: true, userID: 1},
	)
	opt := DefaultTailnetOptions()
	opt.User = "ubuntu"
	opt.Users = map[string]string{"other": "root"}

	machines := tailnetMachines(st, opt)
	if m, _ := machineNamed(machines, "build"); m.Addr != "ubuntu@build.example.ts.net" {
		t.Errorf("the address is %q, want the configured login in front", m.Addr)
	}
	// A machine named in the per-machine table wins over the blanket login.
	if m, _ := machineNamed(machines, "other"); m.Addr != "root@other.example.ts.net" {
		t.Errorf("the address is %q, want the per-machine login", m.Addr)
	}
}

// TestTheAddressFormIsAChoice. The MagicDNS name works everywhere, the short
// name needs a search domain, and the IP needs no DNS at all. Which one is
// right depends on the tailnet, so all three are available.
func TestTheAddressFormIsAChoice(t *testing.T) {
	st := fakeStatus(fakePeer{
		host: "build", dns: "build.example.ts.net.", ip: "100.64.0.1",
		os: "linux", online: true, userID: 1,
	})

	for form, want := range map[string]string{
		TailnetAddrDNS:  "build.example.ts.net",
		TailnetAddrName: "build",
		TailnetAddrIP:   "100.64.0.1",
	} {
		opt := DefaultTailnetOptions()
		opt.Addr = form
		m, _ := machineNamed(tailnetMachines(st, opt), "build")
		if m.Addr != want {
			t.Errorf("addr = %q for form %q, want %q", m.Addr, form, want)
		}
	}
}

// TestTheListIsCappedAmongTheOfferedOnly.
//
// The cap is there so a tailnet with hundreds of machines does not produce a
// candidate list nobody can read. It counts the offered ones, because a cap
// that counted the filtered rows too would offer fewer machines on a tailnet
// full of phones than on one without, which is not a rule anybody could guess.
//
// Negative control: applying the cap to the whole list offers one machine
// here, not two.
func TestTheListIsCappedAmongTheOfferedOnly(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "a-phone", dns: "a-phone.example.ts.net.", os: "iOS", online: true, userID: 1},
		fakePeer{host: "b-build", dns: "b-build.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "c-build", dns: "c-build.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "d-build", dns: "d-build.example.ts.net.", os: "linux", online: true, userID: 1},
	)
	opt := DefaultTailnetOptions()
	opt.Max = 2

	offered := 0
	for _, m := range tailnetMachines(st, opt) {
		if m.Offered {
			offered++
		}
	}
	if offered != 2 {
		t.Errorf("%d machines offered with a cap of 2", offered)
	}
}

// TestTheListIsSortedByName, so two runs read the same. The API returns peers
// in node key order, which is stable and says nothing to a person.
func TestTheListIsSortedByName(t *testing.T) {
	st := fakeStatus(
		fakePeer{host: "zulu", dns: "zulu.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "alpha", dns: "alpha.example.ts.net.", os: "linux", online: true, userID: 1},
		fakePeer{host: "mike", dns: "mike.example.ts.net.", os: "linux", online: true, userID: 1},
	)
	machines := tailnetMachines(st, DefaultTailnetOptions())
	for i := 1; i < len(machines); i++ {
		if machines[i-1].Name > machines[i].Name {
			t.Fatalf("the list is not in name order: %s before %s", machines[i-1].Name, machines[i].Name)
		}
	}
}

// TestAMachineWithNoAddressIsNotOffered. A peer the control plane lists with
// no usable address cannot be typed anywhere, and offering an empty string as
// a host address would write a host entry that reaches nothing.
func TestAMachineWithNoAddressIsNotOffered(t *testing.T) {
	st := fakeStatus(fakePeer{host: "ghost", dns: "", os: "linux", online: true, userID: 1, noAddrs: true})
	opt := DefaultTailnetOptions()
	opt.Addr = TailnetAddrIP

	for _, m := range tailnetMachines(st, opt) {
		if m.Offered {
			t.Errorf("a machine with no address was offered: %+v", m)
		}
	}
}

// TestNoTailnetIsNotAnError for the callers, which all treat it as an empty
// list. A machine with no tailscale on it has to behave exactly as it did
// before any of this existed.
func TestNoTailnetIsNotAnError(t *testing.T) {
	if got := tailnetMachines(nil, DefaultTailnetOptions()); len(got) != 0 {
		t.Errorf("a nil status produced %d machines", len(got))
	}
}
