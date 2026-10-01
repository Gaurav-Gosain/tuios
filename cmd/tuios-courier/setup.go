package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
	"github.com/spf13/cobra"
)

// env is everything a client command needs, loaded once.
type env struct {
	paths  courier.Paths
	cfg    *courier.Config
	keys   *courier.Keys
	store  *courier.Store
	client *courier.Client
}

func loadEnv() (*env, error) {
	p := courier.DefaultPaths()
	cfg, err := courier.LoadConfig(p.ConfigFile())
	if err != nil {
		return nil, err
	}
	keys, err := courier.LoadKeys(p.KeyFile())
	if err != nil {
		return nil, err
	}
	st, err := courier.OpenStore(p.StateDir, nil)
	if err != nil {
		return nil, err
	}
	c, err := courier.NewClient(cfg, keys, st, courier.ClientOptions{})
	if err != nil {
		return nil, err
	}
	return &env{paths: p, cfg: cfg, keys: keys, store: st, client: c}, nil
}

func newInitCmd() *cobra.Command {
	var name, relayURL string
	cmd := &cobra.Command{
		Use:   "init --name NAME --relay URL",
		Short: "Create your identity and configuration",
		Long: `Create your identity key and courier.toml. The key is never replaced:
your peers pinned it, and a new one is a different person to them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := courier.DefaultPaths()
			cfg := &courier.Config{Name: name, Relay: relayURL, ReplyRelease: courier.ReleaseAuto}
			if !courier.ValidName(name) {
				return fmt.Errorf("name %q may hold only letters, digits, '.', '_' and '-'", name)
			}
			if err := courier.ValidateRelayURL(relayURL); err != nil {
				return err
			}
			if _, err := os.Stat(p.ConfigFile()); err == nil {
				return fmt.Errorf("%s already exists; edit it, or remove it and %s to start again", p.ConfigFile(), p.KeyFile())
			}
			keys, err := courier.GenerateKeys()
			if err != nil {
				return err
			}
			if err := courier.SaveKeys(p.KeyFile(), keys); err != nil {
				if errors.Is(err, courier.ErrKeyExists) {
					return fmt.Errorf("%w; remove it only if you mean to become a new identity to every peer", err)
				}
				return err
			}
			if err := cfg.Save(p.ConfigFile()); err != nil {
				return err
			}
			id := keys.Identity()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created identity for %s (fingerprint %s).\n\n", name, id.Fingerprint())
			fmt.Fprintf(out, "Give this line to your teammates and to the relay's operator:\n\n  %s %s\n\n", name, id)
			fmt.Fprintf(out, "Add a teammate with: tuios-courier peers add NAME IDENTITY\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Your name, as your teammates will know you")
	cmd.Flags().StringVar(&relayURL, "relay", "", "The relay URL (https)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("relay")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print your name, identity and relay",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := courier.DefaultPaths()
			cfg, err := courier.LoadConfig(p.ConfigFile())
			if err != nil {
				return err
			}
			keys, err := courier.LoadKeys(p.KeyFile())
			if err != nil {
				return err
			}
			id := keys.Identity()
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]string{
					"name": cfg.Name, "identity": id.String(), "fingerprint": id.Fingerprint(),
					"relay": cfg.Relay, "mailbox": id.MailboxID(),
				})
			}
			fmt.Fprintf(out, "name         %s\nidentity     %s\nfingerprint  %s\nrelay        %s\n", cfg.Name, id, id.Fingerprint(), cfg.Relay)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newPeersCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "peers",
		Short: "List, add and remove the people you exchange mail with",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := courier.LoadConfig(courier.DefaultPaths().ConfigFile())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				type row struct {
					Name        string `json:"name"`
					Identity    string `json:"identity"`
					Fingerprint string `json:"fingerprint"`
					Release     string `json:"release"`
				}
				rows := []row{}
				for _, n := range cfg.PeerNames() {
					p := cfg.Peers[n]
					rows = append(rows, row{n, p.Identity.String(), p.Identity.Fingerprint(), p.Release})
				}
				return json.NewEncoder(out).Encode(map[string]any{"peers": rows})
			}
			if len(cfg.Peers) == 0 {
				fmt.Fprintln(out, "No peers. Add one with: tuios-courier peers add NAME IDENTITY")
				return nil
			}
			for _, n := range cfg.PeerNames() {
				p := cfg.Peers[n]
				fmt.Fprintf(out, "%-16s %s  release=%s\n", n, p.Identity.Fingerprint(), p.Release)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")

	var release string
	add := &cobra.Command{
		Use:   "add NAME IDENTITY",
		Short: "Add a peer by the identity they gave you",
		Long: `Add a peer. Compare the fingerprint this prints with the one they see in
tuios-courier whoami before you trust mail from them.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := courier.DefaultPaths()
			cfg, err := courier.LoadConfig(p.ConfigFile())
			if err != nil {
				return err
			}
			id, err := courier.ParseIdentity(args[1])
			if err != nil {
				return err
			}
			if keys, err := courier.LoadKeys(p.KeyFile()); err == nil && keys.Identity().Equal(id) {
				return errors.New("that identity is your own")
			}
			if err := cfg.AddPeer(args[0], id, release); err != nil {
				return err
			}
			if err := cfg.Save(p.ConfigFile()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s (fingerprint %s, release=%s).\n", args[0], id.Fingerprint(), release)
			return nil
		},
	}
	add.Flags().StringVar(&release, "release", courier.ReleaseHold, "New mail from this peer: hold (you release it) or auto")

	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := courier.DefaultPaths()
			cfg, err := courier.LoadConfig(p.ConfigFile())
			if err != nil {
				return err
			}
			if err := cfg.RemovePeer(args[0]); err != nil {
				return err
			}
			if err := cfg.Save(p.ConfigFile()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(add, remove)
	return cmd
}

// age prints how long ago t was, for a person.
func age(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
