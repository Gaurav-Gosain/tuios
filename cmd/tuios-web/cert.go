package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/sip"
	"github.com/spf13/cobra"
)

// Flags for the certificate sip manages on this user's behalf. They are
// persistent on the root command because --auto-tls and every cert subcommand
// have to mean the same keypair.
var (
	webCertDir     string
	webCertHosts   []string
	webCertDays    int
	webCertForce   bool
	webCertKeyPath bool
)

// certValidity turns --cert-days into a duration, with 0 meaning sip's own
// default of a year.
func certValidity() time.Duration {
	if webCertDays <= 0 {
		return 0
	}
	return time.Duration(webCertDays) * 24 * time.Hour
}

// registerCertFlags adds the flags shared by --auto-tls and the cert commands.
func registerCertFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&webCertDir, "cert-dir", "", "Where --auto-tls keeps its keypair (default: sip's directory in your user config dir)")
	cmd.PersistentFlags().StringSliceVar(&webCertHosts, "cert-host", nil, "Extra DNS name or IP for the --auto-tls certificate (repeatable)")
	cmd.PersistentFlags().IntVar(&webCertDays, "cert-days", 0, "Days an --auto-tls certificate is valid for (0 = 365; under 14 also keeps Chrome's WebTransport path)")
}

// newCertCmd builds the `tuios-web cert` group. It mirrors `sip cert`, because
// the keypair is sip's and a user who read sip's documentation should not have
// to learn a second spelling of the same thing.
//
// Nothing in this group asks a question. tuios-web is run from unit files and
// containers as often as from a shell, and a command that prompts under a
// terminal and does something else under a pipe has two behaviours to reason
// about. Anything destructive wants --force instead, in both.
func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage the self-signed TLS certificate tuios-web serves with",
		Long: `Manage the self-signed TLS certificate tuios-web keeps for this user.

Binding a LAN address requires TLS, and this is the certificate for it when you
do not have one from anywhere else. --auto-tls generates it on first use and
serves from it. It signs for itself, so browsers warn on the first visit;
` + "`tuios-web cert info`" + ` says what the warning looks like and how to stop seeing it.

With no subcommand this prints the certificate's status.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCertInfo(cmd.OutOrStdout())
		},
	}

	info := &cobra.Command{
		Use:     "info",
		Aliases: []string{"status", "show"},
		Short:   "Where the certificate is, what it covers, when it expires",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCertInfo(cmd.OutOrStdout())
		},
	}

	newCmd := &cobra.Command{
		Use:     "new",
		Aliases: []string{"create", "regenerate"},
		Short:   "Generate a certificate, replacing any existing one",
		Long: `Generate a self-signed certificate and key.

The certificate covers localhost, this machine's hostname and hostname.local,
and every non-loopback address on every interface, so it keeps working for the
LAN address a phone actually types. Add more with --cert-host.

Regenerating invalidates what any device has already been told to trust: every
browser that accepted the old certificate asks again, which is why replacing
one takes --force.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCertNew(cmd.OutOrStdout())
		},
	}

	rm := &cobra.Command{
		Use:     "rm",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete the certificate and key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCertRemove(cmd.OutOrStdout())
		},
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the certificate's path and nothing else",
		Long: `Print the certificate's path, for a script or a unit file.

--key prints the private key's path instead. It is not printed anywhere else,
including by ` + "`tuios-web cert info`" + `: tuios-web puts a terminal on a screen that may
be shared or recorded, and a private key's location is not something to
volunteer into that. Ask for it and you get it.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			certFile, keyFile, err := sip.CertPaths(webCertDir)
			if err != nil {
				return err
			}
			if webCertKeyPath {
				fmt.Fprintln(cmd.OutOrStdout(), keyFile)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), certFile)
			return nil
		},
	}
	pathCmd.Flags().BoolVar(&webCertKeyPath, "key", false, "Print the private key's path instead")

	newCmd.Flags().BoolVarP(&webCertForce, "force", "f", false, "Replace an existing certificate")
	rm.Flags().BoolVarP(&webCertForce, "force", "f", false, "Delete without being asked to confirm")

	cmd.AddCommand(info, newCmd, rm, pathCmd)
	return cmd
}

func runCertInfo(w io.Writer) error {
	cert, err := sip.LoadManagedCert(webCertDir)
	if errors.Is(err, fs.ErrNotExist) {
		dir := webCertDir
		if dir == "" {
			d, dirErr := sip.DefaultCertDir()
			if dirErr != nil {
				return dirErr
			}
			dir = d
		}
		fmt.Fprintf(w, "No certificate yet.\n\n  Would live in: %s\n  Generate one:  tuios-web cert new\n  Or have a run generate it: tuios-web --host <address> --auto-tls\n", dir)
		return nil
	}
	if err != nil {
		return err
	}
	printCertSummary(w, cert)
	return nil
}

func runCertNew(w io.Writer) error {
	// Refusing rather than replacing. Whoever ran this asked for something
	// that throws away trust every device has already granted, and the answer
	// to an ambiguous instruction is to stop, not to guess.
	if existing, err := sip.LoadManagedCert(webCertDir); err == nil && !webCertForce {
		return fmt.Errorf("a certificate already exists (valid until %s); pass --force to replace it, and every device that trusted it asks again",
			existing.NotAfter.Format("2006-01-02"))
	}
	cert, err := sip.CreateManagedCert(sip.CertOptions{
		Dir:      webCertDir,
		Hosts:    webCertHosts,
		BindHost: webHost,
		Validity: certValidity(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "Generated a certificate.")
	fmt.Fprintln(w)
	printCertSummary(w, cert)
	return nil
}

func runCertRemove(w io.Writer) error {
	if _, err := sip.LoadManagedCert(webCertDir); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(w, "There is no certificate to remove.")
		return nil
	}
	if !webCertForce {
		return errors.New("pass --force to delete the certificate and its key: every device that trusted it has to be told again")
	}
	if err := sip.RemoveManagedCert(webCertDir); err != nil {
		return err
	}
	fmt.Fprintln(w, "Removed. `tuios-web cert new` makes another one.")
	return nil
}

// printCertSummary says everything needed to use the certificate and nothing
// that helps anyone attack it. The private key's path is deliberately absent;
// see the `tuios-web cert path --key` help.
func printCertSummary(w io.Writer, cert *sip.ManagedCert) {
	fmt.Fprintf(w, "  Certificate: %s\n", cert.CertFile)
	fmt.Fprintf(w, "  Private key: sip.key beside it, readable only by you (0600)\n")
	fmt.Fprintf(w, "  Valid until: %s", cert.NotAfter.Format("2006-01-02 15:04 MST"))
	switch {
	case cert.Expired():
		fmt.Fprint(w, "  (EXPIRED: run `tuios-web cert new --force`)")
	case cert.ExpiresWithin(14 * 24 * time.Hour):
		fmt.Fprint(w, "  (expiring soon)")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Fingerprint: SHA-256 %s\n", cert.Fingerprint)
	fmt.Fprintf(w, "  Covers:      %s\n", strings.Join(append(append([]string{}, cert.DNSNames...), cert.IPs...), ", "))
	fmt.Fprintln(w)
	fmt.Fprintln(w, sip.SelfSignedWarning)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Serve with it:  tuios-web --host <address> --auto-tls")
}
