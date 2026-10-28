package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syslog-gomonitor/internal/engine"
	"syslog-gomonitor/internal/inventory"
)

func TestServeTLSPresentsTheLeaf(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	root := writeChain(t, chainPath, keyPath, false)
	eng := engine.New(inventory.Inventory{MonitorOverdueSeconds: 30, Devices: []inventory.Device{{
		Name: "router", SourceIP: "192.0.2.10", LogPath: filepath.Join(dir, "missing.log"),
		Protocol: "udp", StaleAfterSeconds: 60,
	}}}, engine.Options{})
	eng.Sample(context.Background())
	addr := serveTLS(t, chainPath, keyPath, eng)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    root,
		ServerName: "syslog.example",
	}}}
	res, err := client.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "ok\n" {
		t.Fatalf("body %q", body)
	}
	if res.TLS == nil || len(res.TLS.PeerCertificates) < 2 {
		t.Fatalf("peer chain length %d", len(res.TLS.PeerCertificates))
	}
	if res.TLS.PeerCertificates[0].Subject.CommonName != "syslog.example" {
		t.Fatalf("leaf %s", res.TLS.PeerCertificates[0].Subject)
	}
}

func TestLoadLeafRejectsWrongOrderAndKey(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	writeChain(t, chainPath, keyPath, true)
	if _, err := LoadLeaf(Identity{CertificateFile: chainPath, KeyFile: keyPath}); err == nil {
		t.Fatal("expected chain order error")
	}

	writeChain(t, chainPath, keyPath, false)
	other := filepath.Join(dir, "other.pem")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writeKey(t, other, key)
	if err := os.Chmod(other, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLeaf(Identity{CertificateFile: chainPath, KeyFile: other}); err == nil {
		t.Fatal("expected key mismatch")
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLeaf(Identity{CertificateFile: chainPath, KeyFile: keyPath}); err == nil {
		t.Fatal("expected world-readable key to be refused")
	}
}

func serveTLS(t *testing.T, certPath, keyPath string, eng *engine.Engine) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(eng), ReadHeaderTimeout: 5 * time.Second, TLSConfig: tlsConfig(Identity{
		CertificateFile: certPath, KeyFile: keyPath,
	})}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return ln.Addr().String()
}

func writeChain(t *testing.T, chainPath, keyPath string, reverse bool) *x509.CertPool {
	t.Helper()
	rootKey := mustKey(t)
	rootCert := mustCert(t, &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Example Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}, nil, &rootKey.PublicKey, rootKey)

	intKey := mustKey(t)
	intCert := mustCert(t, &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Example Intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}, rootCert, &intKey.PublicKey, rootKey)

	leafKey := mustKey(t)
	leafCert := mustCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "syslog.example"},
		DNSNames:     []string{"syslog.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, intCert, &leafKey.PublicKey, intKey)

	certs := [][]byte{leafCert.Raw, intCert.Raw}
	if reverse {
		certs = [][]byte{intCert.Raw, leafCert.Raw}
	}
	var pemBytes []byte
	for _, der := range certs {
		pemBytes = append(pemBytes, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := os.WriteFile(chainPath, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	writeKey(t, keyPath, leafKey)
	pool := x509.NewCertPool()
	pool.AddCert(rootCert)
	return pool
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustCert(t *testing.T, tmpl, parent *x509.Certificate, pub any, signer crypto.Signer) *x509.Certificate {
	t.Helper()
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func writeKey(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
