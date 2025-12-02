package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"math/big"
	"time"
)

// CertInfo holds generated certificate information.
type CertInfo struct {
	TLSConfig *tls.Config
	DER       []byte   // DER-encoded certificate
	Hash      [32]byte // SHA-256 hash for serverCertificateHashes
}

// GenerateSelfSignedCert generates a self-signed TLS certificate for WebTransport.
// The certificate is valid for 10 days (Chrome requires < 14 days for serverCertificateHashes).
func GenerateSelfSignedCert(_ string) (*CertInfo, error) {
	// ECDSA P-256 required for serverCertificateHashes
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Certificate validity - must be < 14 days for Chrome WebTransport
	notBefore := time.Now()
	notAfter := notBefore.Add(10 * 24 * time.Hour)

	// Generate serial number
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}
	serial := int64(binary.BigEndian.Uint64(b))
	if serial < 0 {
		serial = -serial
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	parsedCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	certHash := sha256.Sum256(certDER)

	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
		Leaf:        parsedCert, // Required for Chrome WebTransport
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	return &CertInfo{
		TLSConfig: tlsConfig,
		DER:       certDER,
		Hash:      certHash,
	}, nil
}
