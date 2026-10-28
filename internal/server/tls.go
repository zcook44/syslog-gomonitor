package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Identity is the certificate presented to dashboard browsers.
// CertificateFile is a PEM bundle: the server certificate first, then
// intermediates. KeyFile is the matching private key. The process does not
// generate or copy those files.
type Identity struct {
	CertificateFile string
	KeyFile         string
}

// Leaf is the parsed server certificate from the chain file.
type Leaf struct {
	Certificate   *x509.Certificate
	Intermediates int
}

// LoadLeaf reads the chain and key, checks that they match, and checks that
// the file is ordered leaf then intermediates.
func LoadLeaf(id Identity) (Leaf, error) {
	cert, err := loadCertificate(id)
	if err != nil {
		return Leaf{}, err
	}
	return Leaf{Certificate: cert.Leaf, Intermediates: len(cert.Certificate) - 1}, nil
}

func loadCertificate(id Identity) (*tls.Certificate, error) {
	if id.CertificateFile == "" || id.KeyFile == "" {
		return nil, fmt.Errorf("TLS certificate and private key must both be set")
	}
	if err := readableKey(id.KeyFile); err != nil {
		return nil, err
	}
	pair, err := tls.LoadX509KeyPair(id.CertificateFile, id.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("TLS certificate: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("TLS certificate %s contains no certificates", id.CertificateFile)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("TLS server certificate: %w", err)
	}
	if leaf.IsCA {
		return nil, fmt.Errorf("TLS certificate chain must list the server certificate first, then intermediates")
	}
	parsed := []*x509.Certificate{leaf}
	for i, raw := range pair.Certificate[1:] {
		next, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil, fmt.Errorf("TLS intermediate %d: %w", i+1, err)
		}
		if !next.IsCA {
			return nil, fmt.Errorf("TLS certificate %d in the chain is not a CA intermediate", i+2)
		}
		parsed = append(parsed, next)
	}
	for i := 0; i < len(parsed)-1; i++ {
		if err := parsed[i].CheckSignatureFrom(parsed[i+1]); err != nil {
			return nil, fmt.Errorf("TLS certificate %d is not signed by the next certificate in the chain", i+1)
		}
	}
	pair.Leaf = leaf
	return &pair, nil
}

func readableKey(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("TLS private key: %w", err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		return fmt.Errorf("TLS private key %s is accessible to other users", path)
	}
	return nil
}

func tlsConfig(id Identity) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return loadCertificate(id)
		},
	}
}
