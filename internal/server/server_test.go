package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/terminal"
	"web-cursor-agent/internal/users"
)

func TestLoginProjectsAndTerminal(t *testing.T) {
	srv, ts := testServer(t)
	defer ts.Close()
	defer srv.Close()

	res := postJSON(t, ts, "/api/login", `{"username":"alice","password":"secret"}`, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", res.StatusCode)
	}
	cookie := res.Cookies()[0]
	res.Body.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	projects, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(projects.Body)
	projects.Body.Close()
	if projects.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"id":"app"`)) {
		t.Fatalf("projects = %d %s", projects.StatusCode, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/terminal?project=app"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{cookie.Name + "=" + cookie.Value},
			"Origin": []string{ts.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"hi\n"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var output []byte
	for time.Now().Before(deadline) && !bytes.Contains(output, []byte("hi")) {
		readCtx, readCancel := context.WithTimeout(ctx, time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
	}
	if !bytes.Contains(output, []byte("hi")) {
		t.Fatalf("output = %q", output)
	}
}

func TestBuildInfoAndIndexCacheBust(t *testing.T) {
	srv, ts := testServer(t)
	defer ts.Close()
	defer srv.Close()

	staticDir := filepath.Join(srv.cfg.WebDir, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	index := `<!doctype html><link rel="stylesheet" href="/static/app.css" /><script src="/static/build-info.js"></script><script src="/static/app.js"></script>`
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	buildInfo := `{"commit":"abc1234","dirty":true,"builtAt":"2026-01-02T03:04:05Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(staticDir, "build-info.json"), []byte(buildInfo), 0o644); err != nil {
		t.Fatal(err)
	}

	buildRes, err := http.Get(ts.URL + "/api/build")
	if err != nil {
		t.Fatal(err)
	}
	buildBody, _ := io.ReadAll(buildRes.Body)
	buildRes.Body.Close()
	if buildRes.StatusCode != http.StatusOK ||
		!bytes.Contains(buildBody, []byte(`"commit":"abc1234"`)) ||
		!bytes.Contains(buildBody, []byte(`"dirty":true`)) {
		t.Fatalf("build = %d %s", buildRes.StatusCode, buildBody)
	}

	indexRes, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	indexBody, _ := io.ReadAll(indexRes.Body)
	indexRes.Body.Close()
	if indexRes.StatusCode != http.StatusOK ||
		!bytes.Contains(indexBody, []byte(`/static/app.js?v=abc1234-dirty-2026-01-02T03:04:05Z`)) ||
		!bytes.Contains(indexBody, []byte(`/static/app.css?v=abc1234-dirty-2026-01-02T03:04:05Z`)) ||
		!bytes.Contains(indexBody, []byte(`/static/build-info.js?v=abc1234-dirty-2026-01-02T03:04:05Z`)) {
		t.Fatalf("index = %d %s", indexRes.StatusCode, indexBody)
	}
}

func TestRejectsWrongPasswordAndForeignNetwork(t *testing.T) {
	srv, ts := testServer(t)
	defer ts.Close()
	res := postJSON(t, ts, "/api/login", `{"username":"alice","password":"nope"}`, "")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.StatusCode)
	}

	srv.allowsIP = func(net.IP) bool { return false }
	res = postJSON(t, ts, "/api/login", `{"username":"alice","password":"secret"}`, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("同一ネットワーク")) {
		t.Fatalf("status = %d body = %s", res.StatusCode, body)
	}
}

func TestRejectsUnlistedMAC(t *testing.T) {
	srv, ts := testServer(t)
	defer ts.Close()
	srv.allowsIP = func(net.IP) bool { return true }
	srv.remoteIP = func(*http.Request) net.IP { return net.ParseIP("192.168.1.10") }
	srv.lookupMAC = func(net.IP) (string, bool) { return "11:22:33:44:55:66", true }
	res := postJSON(t, ts, "/api/login", `{"username":"alice","password":"secret"}`, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("許可されていません")) {
		t.Fatalf("status = %d body = %s", res.StatusCode, body)
	}
}

func TestMaintenanceRestart(t *testing.T) {
	srv, ts := testServer(t)
	defer ts.Close()
	defer srv.Close()

	res := postJSON(t, ts, "/api/maintenance/restart", `{}`, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d body = %s", res.StatusCode, body)
	}

	login := postJSON(t, ts, "/api/login", `{"username":"alice","password":"secret"}`, "")
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", login.StatusCode)
	}
	cookie := login.Cookies()[0]
	login.Body.Close()
	cookieHdr := cookie.Name + "=" + cookie.Value

	scheduled := 0
	srv.scheduleRestart = func() error {
		scheduled++
		return nil
	}

	res = postJSON(t, ts, "/api/maintenance/restart", `{}`, cookieHdr)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("restart status = %d body = %s", res.StatusCode, body)
	}

	res = postJSON(t, ts, "/api/maintenance/restart", `{}`, cookieHdr)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second restart status = %d body = %s", res.StatusCode, body)
	}
	if scheduled != 1 {
		t.Fatalf("scheduled = %d, want 1", scheduled)
	}
}

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	var file users.File
	if err := file.Upsert("alice", "secret", []string{"aa:bb:cc:dd:ee:ff"}, false, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	usersPath := filepath.Join(dir, "users.yaml")
	if err := users.Save(usersPath, file); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		UsersFile:   usersPath,
		StateDir:    filepath.Join(dir, "var"),
		WebDir:      dir,
		MacCheck:    true,
		DetachGrace: time.Minute,
		Projects: []config.Project{{
			ID:   "app",
			Name: "App",
			Path: project,
		}},
	}
	srv := New(cfg)
	srv.launch = func(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error) {
		return terminal.Start("/bin/cat", nil, project.Path, os.Environ(), cols, rows)
	}
	ts := httptest.NewServer(srv.Handler())
	return srv, ts
}

func postJSON(t *testing.T, ts *httptest.Server, path, payload, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
