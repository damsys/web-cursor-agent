package server

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/localtls"
)

func TestSetupMobileconfigAndPlainHTTPServes(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	paths, _, err := localtls.Init(tlsDir, []string{"wca.lan"}, false)
	if err != nil {
		t.Fatal(err)
	}
	staticDir := filepath.Join(dir, "web", "static")
	vendorDir := filepath.Join(dir, "web", "dist", "vendor", "@xterm", "xterm", "lib")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "xterm.js"), []byte("/*x*/"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Listen:   "127.0.0.1:8787",
		StateDir: dir,
		WebDir:   filepath.Join(dir, "web"),
		TLS: config.TLS{
			Listen:     "127.0.0.1:8788",
			CertFile:   paths.ServerCert,
			KeyFile:    paths.ServerKey,
			CAFile:     paths.CACert,
			PublicHost: "wca.lan",
		},
	}
	srv := New(cfg)
	srv.allowsIP = func(net.IP) bool { return true }
	handler := srv.Handler()

	plainSetup := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/setup", nil)
	plainSetup.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, plainSetup)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "証明書信頼設定") {
		t.Fatal("setup page missing trust instructions")
	}

	mcReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/setup/ca.mobileconfig", nil)
	mcReq.RemoteAddr = "127.0.0.1:12345"
	mcRec := httptest.NewRecorder()
	handler.ServeHTTP(mcRec, mcReq)
	if mcRec.Code != http.StatusOK {
		t.Fatalf("mobileconfig status = %d", mcRec.Code)
	}
	if ct := mcRec.Header().Get("Content-Type"); ct != appleMobileconfigType {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(mcRec.Body)
	if !strings.Contains(string(body), "com.apple.security.root") {
		t.Fatal("mobileconfig body unexpected")
	}

	plainIndex := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/", nil)
	plainIndex.RemoteAddr = "127.0.0.1:12345"
	idxRec := httptest.NewRecorder()
	handler.ServeHTTP(idxRec, plainIndex)
	if idxRec.Code != http.StatusOK {
		t.Fatalf("plain index status = %d body=%s", idxRec.Code, idxRec.Body.String())
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "https://wca.lan:8788/", nil)
	tlsReq.RemoteAddr = "127.0.0.1:12345"
	tlsReq.TLS = &tls.ConnectionState{}
	tlsRec := httptest.NewRecorder()
	handler.ServeHTTP(tlsRec, tlsReq)
	if tlsRec.Code != http.StatusOK {
		t.Fatalf("https index status = %d body=%s", tlsRec.Code, tlsRec.Body.String())
	}

	siteReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/site", nil)
	siteReq.RemoteAddr = "127.0.0.1:12345"
	siteRec := httptest.NewRecorder()
	handler.ServeHTTP(siteRec, siteReq)
	if siteRec.Code != http.StatusOK {
		t.Fatalf("site status = %d", siteRec.Code)
	}
	siteBody := siteRec.Body.String()
	if !strings.Contains(siteBody, `"tlsEnabled":true`) ||
		!strings.Contains(siteBody, `"httpsUrl":"https://wca.lan:8788/"`) ||
		!strings.Contains(siteBody, `"setupUrl":"/setup"`) {
		t.Fatalf("site body = %s", siteBody)
	}
}
