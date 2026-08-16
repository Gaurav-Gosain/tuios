package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/sip"
	"github.com/spf13/cobra"
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

func TestCheckTransportSecurityNamesTheRealFlags(t *testing.T) {
	applyCertFlags(t, certFlags{host: "192.168.1.31", port: "9000"})
	var out bytes.Buffer
	err := checkTransportSecurity(&out)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	advice := out.String()

	for _, want := range []string{
		"--auto-tls",
		"--cert cert.pem --key key.pem",
		"--insecure",
		"tuios-web cert info",
		"192.168.1.31",
		"9000",
	} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice never mentions %q:\n%s", want, advice)
		}
	}
	if strings.Contains(advice, "openssl") {
		t.Errorf("advice still hands out an openssl command:\n%s", advice)
	}
	for _, want := range []string{"--auto-tls", "--cert", "--insecure"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q never mentions %q", err, want)
		}
	}
}

func TestResolveTLSFilesGeneratesOnce(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "127.0.0.1", autoTLS: true, dir: dir})

	var out bytes.Buffer
	certFile, keyFile, err := resolveTLSFiles(&out)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantCert, wantKey, err := sip.CertPaths(dir)
	if err != nil {
		t.Fatalf("cert paths: %v", err)
	}
	if certFile != wantCert || keyFile != wantKey {
		t.Fatalf("served from %s/%s, want %s/%s", certFile, keyFile, wantCert, wantKey)
	}
	if _, err := os.Stat(certFile); err != nil {
		t.Fatalf("certificate was not written: %v", err)
	}
	if !strings.Contains(out.String(), sip.SelfSignedWarning) {
		t.Errorf("first generation never warned about the browser warning:\n%s", out.String())
	}

	// A second run reuses it, and says nothing: the warning is only news once.
	before, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	out.Reset()
	if _, _, err := resolveTLSFiles(&out); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	after, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("second run replaced a certificate that was still good")
	}
	if out.Len() > 0 {
		t.Errorf("second run repeated the warning:\n%s", out.String())
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

func TestResolveTLSFilesDefersToOwnKeypair(t *testing.T) {
	dir := t.TempDir()
	ownCert, ownKey := writeKeypair(t)
	applyCertFlags(t, certFlags{host: "192.168.1.31", cert: ownCert, key: ownKey, autoTLS: true, dir: dir})

	certFile, keyFile, err := resolveTLSFiles(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if certFile != ownCert || keyFile != ownKey {
		t.Fatalf("served from %s/%s, want the keypair passed with --cert/--key", certFile, keyFile)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("generated a certificate anyway: %v %v", entries, err)
	}
}

func TestResolveTLSFilesLeavesTLSOffWithoutAutoTLS(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	certFile, keyFile, err := resolveTLSFiles(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if certFile != "" || keyFile != "" {
		t.Fatalf("picked up %s/%s without --auto-tls", certFile, keyFile)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("generated a certificate without being asked: %v %v", entries, err)
	}
}

// runCert drives the cert group the way a shell does, so the flag wiring is
// under test and not just the functions behind it.
func runCert(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "tuios-web", SilenceUsage: true, SilenceErrors: true}
	registerCertFlags(root)
	root.AddCommand(newCertCmd())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCertInfoWithNoCertificate(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	out, err := runCert(t, "cert", "info", "--cert-dir", dir)
	if err != nil {
		t.Fatalf("cert info: %v", err)
	}
	if !strings.Contains(out, "No certificate yet") {
		t.Errorf("did not say there is none yet:\n%s", out)
	}
	if !strings.Contains(out, dir) {
		t.Errorf("did not say where one would live:\n%s", out)
	}
	if !strings.Contains(out, "tuios-web cert new") {
		t.Errorf("did not say how to make one:\n%s", out)
	}
}

func TestCertNewThenInfo(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "192.168.1.31", dir: dir})

	out, err := runCert(t, "cert", "new", "--cert-dir", dir, "--cert-host", "tuios.lan")
	if err != nil {
		t.Fatalf("cert new: %v", err)
	}
	if !strings.Contains(out, "Generated a certificate") {
		t.Errorf("said nothing about generating one:\n%s", out)
	}
	if !strings.Contains(out, sip.SelfSignedWarning) {
		t.Errorf("cert new never warned about the browser warning:\n%s", out)
	}

	cert, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cert.Covers("tuios.lan") {
		t.Errorf("--cert-host was ignored: DNS %v", cert.DNSNames)
	}

	out, err = runCert(t, "cert", "info", "--cert-dir", dir)
	if err != nil {
		t.Fatalf("cert info: %v", err)
	}
	for _, want := range []string{cert.CertFile, cert.Fingerprint, "tuios.lan", sip.SelfSignedWarning} {
		if !strings.Contains(out, want) {
			t.Errorf("cert info never mentions %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, cert.KeyFile) {
		t.Errorf("cert info printed the private key's path:\n%s", out)
	}
}

func TestCertNewRefusesToReplaceWithoutForce(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	if _, err := runCert(t, "cert", "new", "--cert-dir", dir); err != nil {
		t.Fatalf("cert new: %v", err)
	}
	first, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	_, err = runCert(t, "cert", "new", "--cert-dir", dir)
	if err == nil {
		t.Fatal("replaced an existing certificate without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error %q never names --force", err)
	}
	unchanged, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if unchanged.Fingerprint != first.Fingerprint {
		t.Error("the refused run replaced the certificate anyway")
	}

	if _, err := runCert(t, "cert", "new", "--cert-dir", dir, "--force"); err != nil {
		t.Fatalf("cert new --force: %v", err)
	}
	replaced, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if replaced.Fingerprint == first.Fingerprint {
		t.Error("--force did not replace the certificate")
	}
}

func TestCertPath(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	out, err := runCert(t, "cert", "path", "--cert-dir", dir)
	if err != nil {
		t.Fatalf("cert path: %v", err)
	}
	if got := strings.TrimSpace(out); got != filepath.Join(dir, "sip.crt") {
		t.Errorf("cert path printed %q", got)
	}

	out, err = runCert(t, "cert", "path", "--cert-dir", dir, "--key")
	if err != nil {
		t.Fatalf("cert path --key: %v", err)
	}
	if got := strings.TrimSpace(out); got != filepath.Join(dir, "sip.key") {
		t.Errorf("cert path --key printed %q", got)
	}
}

func TestCertRemove(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	out, err := runCert(t, "cert", "rm", "--cert-dir", dir)
	if err != nil {
		t.Fatalf("cert rm with nothing there: %v", err)
	}
	if !strings.Contains(out, "no certificate to remove") {
		t.Errorf("did not say there was nothing there:\n%s", out)
	}

	if _, err := runCert(t, "cert", "new", "--cert-dir", dir); err != nil {
		t.Fatalf("cert new: %v", err)
	}
	if _, err := runCert(t, "cert", "rm", "--cert-dir", dir); err == nil {
		t.Fatal("deleted the certificate without --force")
	}
	if _, err := sip.LoadManagedCert(dir); err != nil {
		t.Fatalf("the refused run deleted it anyway: %v", err)
	}

	if _, err := runCert(t, "cert", "rm", "--cert-dir", dir, "--force"); err != nil {
		t.Fatalf("cert rm --force: %v", err)
	}
	for _, name := range []string{"sip.crt", "sip.key"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived cert rm --force", name)
		}
	}
}

func TestCertDaysShortensValidity(t *testing.T) {
	dir := t.TempDir()
	applyCertFlags(t, certFlags{host: "localhost", dir: dir})

	if _, err := runCert(t, "cert", "new", "--cert-dir", dir, "--cert-days", "10"); err != nil {
		t.Fatalf("cert new: %v", err)
	}
	cert, err := sip.LoadManagedCert(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Under 14 days is the window Chrome accepts for WebTransport's
	// serverCertificateHashes, which is the only reason to ask for one.
	if !cert.ExpiresWithin(14 * 24 * time.Hour) {
		t.Errorf("--cert-days 10 produced a certificate valid until %s", cert.NotAfter)
	}
}
