package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The tailnet half of address discovery. See internal/federation's tailnet.go
// for the rule this obeys: the machines are offered, never added.

// tailnetProbeTimeout bounds the one local call. It is short because the call
// goes to the tailscaled on this machine over a unix socket, so anything
// slower than this means it is not answering, and a command that hangs while
// completing an argument is worse than one that offers nothing.
const tailnetProbeTimeout = 3 * time.Second

// tailnetOptions is the [tailscale] table, or the defaults when the config
// cannot be read. A config that will not parse must not cost the user the
// suggestion list as well as everything else it costs them.
func tailnetOptions() (federation.TailnetOptions, bool) {
	path, err := config.GetConfigPath()
	if err != nil {
		return federation.DefaultTailnetOptions(), true
	}
	ts, err := config.TailscaleInFile(path)
	if err != nil {
		// A config that will not parse must not cost the user the suggestion
		// list as well as everything else it costs them.
		return federation.DefaultTailnetOptions(), true
	}
	return ts.TailnetOptions(), ts.TailscaleEnabled()
}

// tailnetAddrs are the addresses to offer, empty on a machine with no tailnet
// and empty when the table turns the suggestions off.
func tailnetAddrs() []string {
	opt, enabled := tailnetOptions()
	if !enabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tailnetProbeTimeout)
	defer cancel()
	return federation.TailnetAddrs(ctx, opt)
}

// tailnetAddrFor is the address of one machine by name, for
// 'tuios hosts add NAME --tailnet'.
func tailnetAddrFor(name string) (string, bool) {
	opt, _ := tailnetOptions()
	ctx, cancel := context.WithTimeout(context.Background(), tailnetProbeTimeout)
	defer cancel()
	return federation.TailnetAddrFor(ctx, name, opt)
}

// newHostsTailnetCommand builds 'tuios hosts tailnet'.
func newHostsTailnetCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "tailnet",
		Short: "List the machines on your tailnet and which are offered as addresses",
		Long: `List the machines tuios can see on your tailnet.

Nothing here is added. The list is what 'tuios hosts add' suggests, so you do
not have to remember what a machine is called or type a MagicDNS name by hand.
A host exists because you named it.

Every machine on the tailnet is listed, offered or not, and a machine that is
not offered says why. Phones and tablets are left out because they cannot run
a tuios daemon; a machine that is offline is left out because it cannot be
dialled. Change any of that in the [tailscale] table of your config.

A host added from this list is reached over ssh like every other host. The
tailnet is how the name resolves and how the traffic is carried, and tuios
does not dial a tailnet address itself.

  tuios hosts tailnet                    # what is there
  tuios hosts tailnet --json             # the same, for a script
  tuios hosts add build --tailnet        # add the machine called build`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runHostsTailnet(asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

// tailnetRow is one machine as JSON. The fields are named for what they say
// rather than for the tailscale API, and the reason a machine was left out is
// carried, so a script can tell "no such machine" from "filtered out".
type tailnetRow struct {
	Name    string   `json:"name"`
	Addr    string   `json:"addr"`
	DNSName string   `json:"dns_name"`
	IP      string   `json:"ip,omitempty"`
	OS      string   `json:"os,omitempty"`
	User    string   `json:"user,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Online  bool     `json:"online"`
	Self    bool     `json:"self,omitempty"`
	Shared  bool     `json:"shared,omitempty"`
	Offered bool     `json:"offered"`
	Skipped string   `json:"skipped,omitempty"`
}

func runHostsTailnet(asJSON bool) error {
	opt, enabled := tailnetOptions()
	ctx, cancel := context.WithTimeout(context.Background(), tailnetProbeTimeout)
	defer cancel()

	machines, err := federation.TailnetMachines(ctx, opt)
	if err != nil {
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"machines": []tailnetRow{},
				"enabled":  enabled,
				"error":    err.Error(),
			})
		}
		fmt.Println("No tailnet on this machine.")
		fmt.Println("tuios asks the tailscaled already running here and adds nothing of its own.")
		fmt.Println("Install tailscale and run 'tailscale up', or add hosts by address with 'tuios hosts add'.")
		return nil
	}

	rows := make([]tailnetRow, 0, len(machines))
	for _, m := range machines {
		offered := m.Offered && enabled
		skipped := m.Skipped
		if !enabled && skipped == "" {
			skipped = "suggestions are off in [tailscale]"
		}
		rows = append(rows, tailnetRow{
			Name: m.Name, Addr: m.Addr, DNSName: m.DNSName, IP: m.IP,
			OS: m.OS, User: m.User, Tags: m.Tags, Online: m.Online,
			Self: m.Self, Shared: m.Shared, Offered: offered, Skipped: skipped,
		})
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"machines": rows, "enabled": enabled})
	}

	if len(rows) == 0 {
		fmt.Println("Your tailnet has no machines on it yet.")
		return nil
	}

	configured := configuredHosts()
	offered := 0
	for _, r := range rows {
		already := configured[r.Addr]
		if already == "" && configured[r.Name] != "" {
			// A host of the same name, whatever address it was given. It may
			// be reached another way, by an ssh_config alias for instance, and
			// offering the machine again as if it were new would be wrong.
			already = configured[r.Name]
		}
		mark := " "
		switch {
		case already != "":
			mark = "="
		case r.Offered:
			mark = "+"
			offered++
		}
		note := r.Skipped
		if already != "" {
			note = "already the host " + already
		}
		if note == "" {
			fmt.Printf(" %s %-20s %s\n", mark, r.Name, r.Addr)
			continue
		}
		fmt.Printf(" %s %-20s %-46s %s\n", mark, r.Name, r.Addr, note)
	}
	fmt.Println()
	fmt.Printf("%d machine(s), %d offered as addresses. + is offered, = is already a host.\n", len(rows), offered)
	fmt.Println("Add one with 'tuios hosts add NAME --tailnet'.")
	return nil
}

// configuredHosts maps both the address and the name of every host already in
// the [hosts] table to that host's name, so the listing can say which machines
// are already set up rather than offering them again.
func configuredHosts() map[string]string {
	out := map[string]string{}
	path, err := config.GetConfigPath()
	if err != nil {
		return out
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return out
	}
	for name, h := range hosts {
		out[h.Addr] = name
		out[name] = name
	}
	return out
}
