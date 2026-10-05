// Package tlscert gives the notebook server real HTTPS on the local network.
// Phones only allow location, the offline app shell and secure storage on
// trusted HTTPS, so it creates (once) a small certificate authority for this
// computer and a server certificate for its names and LAN addresses. Installing
// ca.crt on the phone once makes every later certificate trusted, like mkcert.
package tlscert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Files struct {
	Cert, Key string
	// CA is the authority in DER form, the format phones install.
	CA string
	// Hosts are the names and addresses the certificate is valid for.
	Hosts []string
}

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour // the longest browsers accept for a server certificate
	renewBefore    = 30 * 24 * time.Hour
)

// Ensure returns usable files in dir, creating or renewing them as needed: a
// new LAN address or a certificate close to expiry triggers a new server
// certificate signed by the same authority, so the phone never reinstalls it.
func Ensure(dir string) (Files, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Files{}, err
	}
	files := Files{Cert: filepath.Join(dir, "cert.pem"), Key: filepath.Join(dir, "key.pem"), CA: filepath.Join(dir, "ca.crt")}
	caCert, caKey, err := authority(dir)
	if err != nil {
		return Files{}, fmt.Errorf("certificate authority: %w", err)
	}
	if err := os.WriteFile(files.CA, caCert.Raw, 0o644); err != nil {
		return Files{}, err
	}
	names, ips := localNames()
	for _, ip := range ips {
		files.Hosts = append(files.Hosts, ip.String())
	}
	files.Hosts = append(files.Hosts, names...)
	if current(files, caCert, names, ips) {
		return files, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Files{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{Organization: []string{"Bairro em Ação"}, CommonName: names[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return Files{}, err
	}
	if err := writePEM(files.Cert, "CERTIFICATE", der, 0o644); err != nil {
		return Files{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Files{}, err
	}
	return files, writePEM(files.Key, "EC PRIVATE KEY", keyDER, 0o600)
}

func authority(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	if certPEM, err := os.ReadFile(certPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, err
		}
		certBlock, _ := pem.Decode(certPEM)
		keyBlock, _ := pem.Decode(keyPEM)
		if certBlock == nil || keyBlock == nil {
			return nil, nil, errors.New("unreadable files; delete them to create a new authority")
		}
		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, nil, err
		}
		key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		return cert, key, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{Organization: []string{"Bairro em Ação"}, CommonName: "Bairro em Ação · " + host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// current reports whether the stored server certificate is signed by ca,
// covers every name and address and is not about to expire.
func current(files Files, ca *x509.Certificate, names []string, ips []net.IP) bool {
	pair, err := tls.LoadX509KeyPair(files.Cert, files.Key)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || cert.CheckSignatureFrom(ca) != nil || time.Until(cert.NotAfter) < renewBefore {
		return false
	}
	for _, name := range names {
		if !slices.Contains(cert.DNSNames, name) {
			return false
		}
	}
	for _, ip := range ips {
		if !slices.ContainsFunc(cert.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

// localNames lists what a phone may type to reach this computer.
func localNames() ([]string, []net.IP) {
	names := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" && host != "localhost" {
		names = append(names, host, strings.TrimSuffix(host, ".local")+".local")
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if n, ok := addr.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				ips = append(ips, n.IP.To4())
			}
		}
	}
	slices.SortFunc(ips, func(a, b net.IP) int { return bytes.Compare(a.To16(), b.To16()) })
	return names, slices.CompactFunc(ips, net.IP.Equal)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), mode)
}
