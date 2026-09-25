package main

import (
	"bytes"
	"testing"

	"github.com/Gaurav-Gosain/sip"
)

// certFlags is the TLS half of the command line, set for one test and put back
// afterwards. The flags are package globals because cobra writes into them, so
// a test that forgot to restore one would decide the next test's outcome.
type certFlags struct {
	host     string
	port     string
	cert     string
	key      string
	autoTLS  bool
	insecure bool
	dir      string
	hosts    []string
	days     int
	force    bool
}

func applyCertFlags(t *testing.T, f certFlags) {
	t.Helper()
	saved := certFlags{webHost, webPort, webTLSCert, webTLSKey, webAutoTLS, webInsecure, webCertDir, webCertHosts, webCertDays, webCertForce}
	t.Cleanup(func() {
		webHost, webPort = saved.host, saved.port
		webTLSCert, webTLSKey = saved.cert, saved.key
		webAutoTLS, webInsecure = saved.autoTLS, saved.insecure
		webCertDir, webCertHosts, webCertDays = saved.dir, saved.hosts, saved.days
		webCertForce = saved.force
	})
	if f.port == "" {
		f.port = "7681"
	}
	webHost, webPort = f.host, f.port
	webTLSCert, webTLSKey = f.cert, f.key
	webAutoTLS, webInsecure = f.autoTLS, f.insecure
	webCertDir, webCertHosts, webCertDays = f.dir, f.hosts, f.days
	webCertForce = f.force
}

// writeKeypair drops a real keypair somewhere other than the managed cert dir,
// standing in for one the user brought themselves.
func writeKeypair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	cert, err := sip.CreateManagedCert(sip.CertOptions{Dir: dir})
	if err != nil {
		t.Fatalf("create keypair: %v", err)
	}
	return cert.CertFile, cert.KeyFile
}

func TestCheckTransportSecurity(t *testing.T) {
	ownCert, ownKey := writeKeypair(t)

	tests := []struct {
		name    string
		flags   certFlags
		wantErr bool
	}{
		{"loopback needs nothing", certFlags{host: "localhost"}, false},
		{"empty host is loopback", certFlags{host: ""}, false},
		{"127.0.0.1 needs nothing", certFlags{host: "127.0.0.1"}, false},
		{"LAN bind in clear text refuses", certFlags{host: "192.168.1.31"}, true},
		{"wildcard bind in clear text refuses", certFlags{host: "0.0.0.0"}, true},
		{"auto-tls satisfies it", certFlags{host: "192.168.1.31", autoTLS: true}, false},
		{"own keypair satisfies it", certFlags{host: "192.168.1.31", cert: ownCert, key: ownKey}, false},
		{"insecure satisfies it", certFlags{host: "192.168.1.31", insecure: true}, false},
		{"cert without key refuses", certFlags{host: "localhost", cert: ownCert}, true},
		{"key without cert refuses", certFlags{host: "localhost", key: ownKey}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyCertFlags(t, tt.flags)
			var out bytes.Buffer
			err := checkTransportSecurity(&out)
			if tt.wantErr && err == nil {
				t.Fatalf("expected a refusal, got none")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no refusal, got %v", err)
			}
			if !tt.wantErr && out.Len() > 0 {
				t.Fatalf("printed advice for a bind it accepted: %q", out.String())
			}
		})
	}
}

func TestResolveTLSFilesCoversTheBindAddress(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "192.168.1.31", autoTLS: true, dir: dir, hosts: []string{"tuios.lan"}})

	if _, _, err := resolveTLSFiles(&bytes.Buffer{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cert, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, host := range []string{"192.168.1.31", "tuios.lan", "localhost", "127.0.0.1"} {
		if !cert.Covers(host) {
			t.Errorf("certificate does not sign for %s (DNS %v, IP %v)", host, cert.DNSNames, cert.IPs)
		}
	}
}
