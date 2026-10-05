package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCreatesATrustChainAndReusesIt(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	caDER, err := os.ReadFile(first.CA)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil || !ca.IsCA {
		t.Fatalf("ca.crt is not a CA certificate: %v", err)
	}
	pair, err := tls.LoadX509KeyPair(first.Cert, first.Key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	// What the phone does once ca.crt is installed: the server certificate verifies for every LAN address.
	for _, host := range first.Hosts {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}

	before, _ := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if _, err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if string(before) != string(after) {
		t.Error("a valid certificate must be reused, not reissued on every start")
	}

	// Losing the server certificate (or a new IP) reissues it with the same authority.
	_ = os.Remove(filepath.Join(dir, "cert.pem"))
	again, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	caAgain, _ := os.ReadFile(again.CA)
	if string(caAgain) != string(caDER) {
		t.Error("the authority changed; the phone would have to install it again")
	}
}
