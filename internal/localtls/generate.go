// Package localtls は家庭内 HTTPS 用の自家 CA とサーバ証明書を扱う。
package localtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	caCertName       = "ca.crt"
	caKeyName        = "ca.key"
	serverCertName   = "server.crt"
	serverKeyName    = "server.key"
	mobileconfigName = "ca.mobileconfig"
	caCommonName     = "web-cursor-agent Local CA"
	serverCommonName = "web-cursor-agent"
	caValidFor       = 10 * 365 * 24 * time.Hour
	serverValidFor   = 825 * 24 * time.Hour
	serialNumberBits = 128
)

// Paths は TLS 関連ファイルの配置である。
type Paths struct {
	Dir          string
	CACert       string
	CAKey        string
	ServerCert   string
	ServerKey    string
	Mobileconfig string
}

// DirPaths はディレクトリ内の既定ファイル名を返す。
func DirPaths(dir string) Paths {
	return Paths{
		Dir:          dir,
		CACert:       filepath.Join(dir, caCertName),
		CAKey:        filepath.Join(dir, caKeyName),
		ServerCert:   filepath.Join(dir, serverCertName),
		ServerKey:    filepath.Join(dir, serverKeyName),
		Mobileconfig: filepath.Join(dir, mobileconfigName),
	}
}

// Init は自家 CA とサーバ証明書、構成プロファイルを生成して書き出す。
// force が false で必須ファイルが揃っているときは生成せず skipped=true を返す。
// 一部だけ存在するときはエラーにする。
func Init(dir string, hosts []string, force bool) (paths Paths, skipped bool, err error) {
	paths = DirPaths(dir)
	if !force {
		complete, any, err := inspectExisting(paths)
		if err != nil {
			return Paths{}, false, err
		}
		if complete {
			return paths, true, nil
		}
		if any {
			return Paths{}, false, fmt.Errorf("incomplete TLS files in %s; remove them or use --force", dir)
		}
	}
	hosts, err = normalizeHosts(hosts)
	if err != nil {
		return Paths{}, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Paths{}, false, fmt.Errorf("create tls dir: %w", err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Paths{}, false, fmt.Errorf("generate CA key: %w", err)
	}
	caCert, err := createCACertificate(caKey)
	if err != nil {
		return Paths{}, false, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Paths{}, false, fmt.Errorf("generate server key: %w", err)
	}
	serverCert, err := createServerCertificate(hosts, serverKey, caCert, caKey)
	if err != nil {
		return Paths{}, false, err
	}
	if err := writeCertificateFiles(paths, caCert, caKey, serverCert, serverKey); err != nil {
		return Paths{}, false, err
	}
	return paths, false, nil
}

// PrimaryDNSName はサーバ証明書の最初の DNS SAN を返す。
func PrimaryDNSName(serverCertPath string) (string, error) {
	raw, err := os.ReadFile(serverCertPath)
	if err != nil {
		return "", fmt.Errorf("read server cert: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("invalid server cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse server cert: %w", err)
	}
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0], nil
	}
	if cert.Subject.CommonName != "" && net.ParseIP(cert.Subject.CommonName) == nil {
		return cert.Subject.CommonName, nil
	}
	return "", fmt.Errorf("server certificate has no DNS name")
}

// ReissueServer は既存の CA でサーバ証明書だけを作り直す。
func ReissueServer(dir string, hosts []string) (Paths, error) {
	hosts, err := normalizeHosts(hosts)
	if err != nil {
		return Paths{}, err
	}
	paths := DirPaths(dir)
	caCert, caKey, err := loadCA(paths)
	if err != nil {
		return Paths{}, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Paths{}, fmt.Errorf("generate server key: %w", err)
	}
	serverCert, err := createServerCertificate(hosts, serverKey, caCert, caKey)
	if err != nil {
		return Paths{}, err
	}
	if err := writePEM(paths.ServerCert, "CERTIFICATE", serverCert.Raw, 0o644); err != nil {
		return Paths{}, err
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return Paths{}, fmt.Errorf("marshal server key: %w", err)
	}
	if err := writePEM(paths.ServerKey, "EC PRIVATE KEY", serverKeyDER, 0o600); err != nil {
		return Paths{}, err
	}
	profile, err := Mobileconfig(caCert.Raw)
	if err != nil {
		return Paths{}, err
	}
	if err := os.WriteFile(paths.Mobileconfig, profile, 0o644); err != nil {
		return Paths{}, fmt.Errorf("write mobileconfig: %w", err)
	}
	return paths, nil
}

func writeCertificateFiles(paths Paths, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, serverCert *x509.Certificate, serverKey *ecdsa.PrivateKey) error {
	if err := writePEM(paths.CACert, "CERTIFICATE", caCert.Raw, 0o644); err != nil {
		return err
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	if err := writePEM(paths.CAKey, "EC PRIVATE KEY", caKeyDER, 0o600); err != nil {
		return err
	}
	if err := writePEM(paths.ServerCert, "CERTIFICATE", serverCert.Raw, 0o644); err != nil {
		return err
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return fmt.Errorf("marshal server key: %w", err)
	}
	if err := writePEM(paths.ServerKey, "EC PRIVATE KEY", serverKeyDER, 0o600); err != nil {
		return err
	}
	profile, err := Mobileconfig(caCert.Raw)
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.Mobileconfig, profile, 0o644); err != nil {
		return fmt.Errorf("write mobileconfig: %w", err)
	}
	return nil
}

func createCACertificate(key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   caCommonName,
			Organization: []string{"web-cursor-agent"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(caValidFor),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	return cert, nil
}

func createServerCertificate(hosts []string, key *ecdsa.PrivateKey, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   serverCommonName,
			Organization: []string{"web-cursor-agent"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(serverValidFor),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}
		template.DNSNames = append(template.DNSNames, host)
	}
	if len(template.DNSNames) == 0 && len(template.IPAddresses) == 0 {
		return nil, fmt.Errorf("hosts must include at least one DNS name or IP")
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse server certificate: %w", err)
	}
	return cert, nil
}

func loadCA(paths Paths) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(paths.CACert)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(paths.CAKey)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA key: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("invalid CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA key: %w", err)
	}
	return cert, key, nil
}

// LoadCACertificateDER は PEM の CA 証明書から DER を取り出す。
func LoadCACertificateDER(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid CA cert PEM")
	}
	return block.Bytes, nil
}

func normalizeHosts(hosts []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	hasDNS := false
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
		if net.ParseIP(host) == nil {
			hasDNS = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one --hosts value is required")
	}
	if !hasDNS {
		return nil, fmt.Errorf("hosts must include at least one DNS name (IP-only certificates are not recommended)")
	}
	return out, nil
}

// inspectExisting は必須証明書 4 ファイルの有無を見る。
// complete は 4 つすべてあるとき、any は必須または mobileconfig のいずれかがあるとき。
func inspectExisting(paths Paths) (complete bool, any bool, err error) {
	essential := []string{paths.CACert, paths.CAKey, paths.ServerCert, paths.ServerKey}
	essentialCount := 0
	for _, path := range essential {
		exists, err := pathExists(path)
		if err != nil {
			return false, false, err
		}
		if exists {
			essentialCount++
			any = true
		}
	}
	mcExists, err := pathExists(paths.Mobileconfig)
	if err != nil {
		return false, false, err
	}
	if mcExists {
		any = true
	}
	return essentialCount == len(essential), any, nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialNumberBits))
	if err != nil {
		return nil, fmt.Errorf("serial number: %w", err)
	}
	return serial, nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer file.Close()
	if err := pem.Encode(file, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return nil
}
