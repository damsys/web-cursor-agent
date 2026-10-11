package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadResolvesPathsAndDefaultsMacCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
listen: "127.0.0.1:8787"
projects:
  - id: app
    path: ./work
allow_cidrs:
  - "10.0.0.0/8"
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MacCheck {
		t.Fatal("mac check should default to enabled")
	}
	if cfg.DetachGrace != defaultDetachGrace {
		t.Fatalf("detach grace = %s", cfg.DetachGrace)
	}
	if cfg.UsersFile != filepath.Join(dir, "users.yaml") {
		t.Fatalf("users file = %s", cfg.UsersFile)
	}
	if cfg.Projects[0].Path != filepath.Join(dir, "work") {
		t.Fatalf("project path = %s", cfg.Projects[0].Path)
	}
	if cfg.Projects[0].Name != "app" {
		t.Fatalf("name = %s", cfg.Projects[0].Name)
	}
	if len(cfg.Networks) != 1 {
		t.Fatalf("networks = %d", len(cfg.Networks))
	}
}

func TestLoadDetachGrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("detach_grace: 15m\nprojects: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DetachGrace != 15*time.Minute {
		t.Fatalf("detach grace = %s", cfg.DetachGrace)
	}
}

func TestLoadCanDisableMacCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("mac_check: false\nprojects: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MacCheck {
		t.Fatal("mac check should be disabled")
	}
}

func TestLoadAutoRenameDefaultsEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("projects: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoRename.Enabled {
		t.Fatal("auto_rename should default to enabled")
	}
}

func TestLoadCanDisableAutoRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte("auto_rename:\n  enabled: false\nprojects: []\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoRename.Enabled {
		t.Fatal("auto_rename should be disabled")
	}
}

func TestLoadRejectsDuplicateProjectID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
projects:
  - id: app
    path: /tmp/a
  - id: app
    path: /tmp/b
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestLoadTLS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
projects: []
tls:
  cert_file: ./tls/server.crt
  key_file: ./tls/server.key
  ca_file: ./tls/ca.crt
  public_host: wca.lan
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS.Enabled() {
		t.Fatal("TLS should be enabled")
	}
	if cfg.TLS.CertFile != filepath.Join(dir, "tls", "server.crt") {
		t.Fatalf("cert = %s", cfg.TLS.CertFile)
	}
	if cfg.TLS.PublicHost != "wca.lan" {
		t.Fatalf("host = %s", cfg.TLS.PublicHost)
	}
	port, err := cfg.ListenPort()
	if err != nil {
		t.Fatal(err)
	}
	if port != "8787" {
		t.Fatalf("port = %s", port)
	}
	if cfg.TLS.Listen != "0.0.0.0:8788" {
		t.Fatalf("tls.listen = %s", cfg.TLS.Listen)
	}
	tlsPort, err := cfg.TLSListenPort()
	if err != nil {
		t.Fatal(err)
	}
	if tlsPort != "8788" {
		t.Fatalf("tls port = %s", tlsPort)
	}
}

func TestLoadTLSRejectsSameListenPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
listen: "127.0.0.1:8787"
projects: []
tls:
  listen: "127.0.0.1:8787"
  cert_file: ./tls/server.crt
  key_file: ./tls/server.key
  public_host: wca.lan
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected same-port error")
	}
}

func TestLoadTLSRequiresPublicHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
projects: []
tls:
  cert_file: ./tls/server.crt
  key_file: ./tls/server.key
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected public_host error")
	}
}

func TestLoadTLSRejectsPartialFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
projects: []
tls:
  cert_file: ./tls/server.crt
  public_host: wca.lan
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected cert/key pair error")
	}
}
