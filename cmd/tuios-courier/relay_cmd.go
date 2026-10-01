package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier/relay"
	"github.com/spf13/cobra"
)

func newRelayCmd() *cobra.Command {
	var addr, tlsCert, tlsKey string
	var insecure bool
	var opts relay.Options
	cmd := &cobra.Command{
		Use:   "relay --roster FILE",
		Short: "Run a relay for the identities on a roster",
		Long: `Run a relay: an HTTPS mailbox that holds sealed mail for the identities on
its roster until they fetch it. It cannot read the mail.

The roster is a file of NAME IDENTITY lines. It is read again when it changes.
Bind to loopback, give --tls-cert and --tls-key, or pass --insecure when an
ingress in front of the relay terminates TLS.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			useTLS := tlsCert != "" || tlsKey != ""
			if useTLS && (tlsCert == "" || tlsKey == "") {
				return errors.New("--tls-cert and --tls-key go together")
			}
			if err := relay.CheckBind(addr, useTLS, insecure); err != nil {
				return err
			}
			logger := log.New(cmd.ErrOrStderr(), "tuios-courier relay: ", log.LstdFlags)
			opts.Logf = logger.Printf
			srv, err := relay.New(opts)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			hs := srv.HTTPServer(addr)
			scheme := "http"
			if useTLS {
				scheme = "https"
			}
			logger.Printf("listening on %s://%s%s/", scheme, ln.Addr(), opts.Prefix)

			ctx := cmd.Context()
			go func() {
				t := time.NewTicker(time.Minute)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						srv.Sweep()
					}
				}
			}()
			go func() {
				<-ctx.Done()
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(sctx)
			}()
			if useTLS {
				err = hs.ServeTLS(ln, tlsCert, tlsKey)
			} else {
				err = hs.Serve(ln)
			}
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("relay: %w", err)
		},
	}
	f := cmd.Flags()
	f.StringVar(&addr, "addr", "127.0.0.1:8080", "Address to listen on")
	f.StringVar(&opts.Roster, "roster", "", "File of the identities this relay serves (required)")
	f.StringVar(&opts.DataDir, "data", "", "Directory that keeps mail across restarts (default: memory only)")
	f.StringVar(&opts.Prefix, "prefix", "", "Path the relay is served under, when an ingress does not strip it")
	f.DurationVar(&opts.TTL, "ttl", relay.DefaultTTL, "How long a message waits to be fetched (at most 168h)")
	f.StringVar(&tlsCert, "tls-cert", "", "TLS certificate file")
	f.StringVar(&tlsKey, "tls-key", "", "TLS key file")
	f.BoolVar(&insecure, "insecure", false, "Serve clear HTTP on a network address, behind an ingress that terminates TLS")
	_ = cmd.MarkFlagRequired("roster")
	return cmd
}
