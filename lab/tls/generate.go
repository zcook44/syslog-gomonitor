// Generate a lab CA and a server certificate for the dashboard.
// The private key is mode 0600. Do not commit lab/tls/out.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("out", "lab/tls/out", "directory for ca.pem, fullchain.pem, and privkey.pem")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	names, ips := identities()
	caKey, caCert := makeCA()
	leafKey, leafCert := makeLeaf(caCert, caKey, names, ips)
	writeCert(filepath.Join(*out, "ca.pem"), caCert.Raw)
	writeCert(filepath.Join(*out, "fullchain.pem"), leafCert.Raw, caCert.Raw)
	writeKey(filepath.Join(*out, "privkey.pem"), leafKey)
	fmt.Printf("CA: %s\n", filepath.Join(*out, "ca.pem"))
	fmt.Printf("Certificate names: %s\n", joinNames(names, ips))
	fmt.Println("Dashboard:")
	fmt.Println("  https://127.0.0.1:8443/")
	for _, ip := range ips {
		if ip.IsLoopback() {
			continue
		}
		fmt.Printf("  https://%s:8443/\n", ip)
	}
	fmt.Println("Trust ca.pem on each workstation, or the browser will warn that the certificate is unknown.")
}

func identities() ([]string, []net.IP) {
	names := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" && host != "localhost" {
		names = append(names, host)
	}
	ips := []net.IP{net.ParseIP("127.0.0.1")}
	ifaces, err := net.Interfaces()
	if err != nil {
		fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || !ip.IsGlobalUnicast() {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return names, ips
}

func makeCA() (*ecdsa.PrivateKey, *x509.Certificate) {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "syslog-gomonitor lab"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	return key, mustCert(tmpl, tmpl, &key.PublicKey, key)
}

func makeLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string, ips []net.IP) (*ecdsa.PrivateKey, *x509.Certificate) {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		IPAddresses:  ips,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	return key, mustCert(tmpl, ca, &key.PublicKey, caKey)
}

func mustKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}
	return key
}

func mustCert(tmpl, parent *x509.Certificate, pub any, signer *ecdsa.PrivateKey) *x509.Certificate {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		fatal(err)
	}
	return cert
}

func writeCert(path string, ders ...[]byte) {
	var buf []byte
	for _, der := range ders {
		buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		fatal(err)
	}
}

func writeKey(path string, key *ecdsa.PrivateKey) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		fatal(err)
	}
}

func joinNames(names []string, ips []net.IP) string {
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	for _, ip := range ips {
		out += ", " + ip.String()
	}
	return out
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "lab tls: %v\n", err)
	os.Exit(1)
}
