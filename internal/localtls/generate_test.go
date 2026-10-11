package localtls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAndReissue(t *testing.T) {
	dir := t.TempDir()
	paths, skipped, err := Init(dir, []string{"wca.lan", "127.0.0.1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if skipped {
		t.Fatal("first init should generate")
	}
	caDER, err := LoadCACertificateDER(paths.CACert)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	if !caCert.IsCA {
		t.Fatal("CA should be marked as CA")
	}
	serverPEM, err := os.ReadFile(paths.ServerCert)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(serverPEM)
	serverCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := serverCert.VerifyHostname("wca.lan"); err != nil {
		t.Fatalf("hostname: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := serverCert.Verify(x509.VerifyOptions{DNSName: "wca.lan", Roots: roots}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	profile, err := os.ReadFile(paths.Mobileconfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(profile), "com.apple.security.root") {
		t.Fatal("mobileconfig missing root payload")
	}
	again, skipped, err := Init(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("second init should skip")
	}
	if again.ServerCert != paths.ServerCert {
		t.Fatalf("paths = %#v", again)
	}
	name, err := PrimaryDNSName(paths.ServerCert)
	if err != nil || name != "wca.lan" {
		t.Fatalf("primary DNS = %q %v", name, err)
	}
	if _, err := ReissueServer(dir, []string{"wca.lan"}); err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(paths.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if _, err := x509.ParseECPrivateKey(keyBlock.Bytes); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeHostsRequiresDNS(t *testing.T) {
	if _, _, err := Init(t.TempDir(), []string{"127.0.0.1"}, false); err == nil {
		t.Fatal("expected DNS name requirement")
	}
}

func TestMobileconfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	paths, _, err := Init(dir, []string{"example.lan"}, false)
	if err != nil {
		t.Fatal(err)
	}
	der, err := LoadCACertificateDER(paths.CACert)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Mobileconfig(der)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<data>") {
		t.Fatal("missing data block")
	}
	if filepath.Base(paths.Mobileconfig) != "ca.mobileconfig" {
		t.Fatalf("name = %s", paths.Mobileconfig)
	}
}
