package federation

import (
	"net"
	"strings"
)

// LocalAddr is one address another machine may reach this one on.
type LocalAddr struct {
	// Host is what the other machine dials: a MagicDNS name or an IP.
	Host string
	// Bind is the IP this machine holds, which a listener binds.
	Bind string
	// Tailnet says the address is this machine's on its tailnet.
	Tailnet bool
	// Public says the address can be reached from the internet.
	Public bool
}

// LocalAddrs finds the addresses of this machine: its MagicDNS name when
// machines names one, then at most lanLimit LAN IPv4 addresses.
func LocalAddrs(machines []TailnetMachine, lanLimit int) []LocalAddr {
	var out []LocalAddr
	for _, m := range machines {
		if !m.Self || m.IP == "" {
			continue
		}
		host := m.DNSName
		if host == "" {
			host = m.IP
		}
		out = append(out, LocalAddr{Host: host, Bind: m.IP, Tailnet: true})
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	ifaces, _ := net.Interfaces()
	lan := 0
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || skipLocalInterface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) || lan >= lanLimit {
				continue
			}
			out = append(out, LocalAddr{Host: ip.String(), Bind: ip.String(), Public: PublicIP(ip)})
			lan++
		}
	}
	return out
}

// PublicIP reports whether ip can be reached from the internet: a global
// unicast address that is not private (RFC 1918, fc00::/7) and not in the
// shared range tailnets use (100.64.0.0/10).
func PublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return !cgnat.Contains(ip)
}

// skipLocalInterface leaves out the interfaces of containers, VMs and VPNs: a
// phone cannot reach them, and each address makes a pairing code bigger.
func skipLocalInterface(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "podman", "cni", "flannel", "tailscale", "utun", "zt", "vmnet", "vboxnet", "lxc", "lxd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
