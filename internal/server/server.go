// Package server はログイン、プロジェクト一覧、仮想端末の中継を提供する。
package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/security"
	"web-cursor-agent/internal/terminal"
)

const (
	cookieName    = "wca_session"
	sessionTTL    = 30 * 24 * time.Hour
	maxSessions   = 4
	maxInputBytes = 256 * 1024
	loginFailures = 8
	loginWindow   = 5 * time.Minute
)

// LaunchFunc はユーザーの認証ディレクトリで対話プロセスを起動する。
type LaunchFunc func(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error)

// Server は HTTP と WebSocket の入口である。
type Server struct {
	cfg       config.Config
	allowsIP  func(net.IP) bool
	lookupMAC func(net.IP) (string, bool)
	remoteIP  func(*http.Request) net.IP
	launch    LaunchFunc
	// scheduleRestart はテストで差し替え可能。nil のときは systemd-run による実再起動。
	scheduleRestart func() error
	// nameGenerator は自動リネーム用。nil のときは agent -p を使う。
	nameGenerator func(context.Context, nameGenRequest) (string, error)
	sessions      *sessionStore
	runtimes      *runtimeHub
	limiter       *loginLimiter

	restartMu      sync.Mutex
	restartPending bool
}

// New は設定に沿ったサーバを作る。テストは nil の関数を本番の実装に置き換わる既定値として扱う。
func New(cfg config.Config) *Server {
	server := &Server{
		cfg: cfg,
		remoteIP: func(r *http.Request) net.IP {
			return security.ParseRemoteAddr(r.RemoteAddr)
		},
		lookupMAC: security.LookupMAC,
		launch:    launchAgent,
		sessions:  newSessionStore(filepath.Join(cfg.StateDir, "sessions.json")),
		runtimes:  newRuntimeHub(maxSessions),
		limiter:   newLoginLimiter(loginFailures, loginWindow),
	}
	server.scheduleRestart = server.scheduleServiceRestart
	server.allowsIP = func(ip net.IP) bool {
		ok, err := security.SameSegment(ip, cfg.Networks)
		if err != nil {
			log.Printf("segment check failed: %v", err)
			return false
		}
		return ok
	}
	return server
}

// Handler はセグメント制限と Origin 確認を通した HTTP ハンドラを返す。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /api/site", s.handleSite)
	mux.HandleFunc("GET /api/build", s.handleBuild)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/projects/{id}/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/projects/{id}/sessions/{chatId}/hide", s.handleHideSession)
	mux.HandleFunc("POST /api/maintenance/restart", s.handleMaintenanceRestart)
	mux.HandleFunc("GET /ws/terminal", s.handleTerminal)
	mux.HandleFunc("GET /setup", s.handleSetupIndex)
	mux.HandleFunc("GET /setup/", s.handleSetupIndex)
	mux.HandleFunc("GET /setup/ca.mobileconfig", s.handleSetupMobileconfig)
	mux.HandleFunc("GET /setup/ca.cer", s.handleSetupCACer)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle(
		"GET /static/",
		http.StripPrefix("/static/", noCacheStatic(http.FileServer(http.Dir(s.cfg.StaticDir())))),
	)
	mux.Handle("GET /vendor/", http.StripPrefix("/vendor/", http.FileServer(http.Dir(s.cfg.VendorDir()))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ip := s.remoteIP(r)
		if !s.allowsIP(ip) {
			writeClientError(w, r, http.StatusForbidden, "同一ネットワークからのアクセスのみ許可されています")
			return
		}
		if !security.CheckOrigin(r.Method, r.URL.Path, r.Header.Get("Origin"), r.Host) {
			writeClientError(w, r, http.StatusForbidden, "別のサイトからのリクエストは受け付けていません")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Close は中継中の対話プロセスを終了する。
func (s *Server) Close() {
	s.runtimes.CloseAll()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// ビルド版を query に載せ、iOS Safari などが古い app.js/css を掴み続けるのを避ける。
	data, err := os.ReadFile(filepath.Join(s.cfg.StaticDir(), "index.html"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	version := s.buildInfo().cacheVersion()
	html := string(data)
	html = strings.ReplaceAll(html, "__WCA_V__", version)
	html = strings.ReplaceAll(
		html,
		`href="/static/app.css"`,
		`href="/static/app.css?v=`+version+`"`,
	)
	html = strings.ReplaceAll(
		html,
		`src="/static/build-info.js"`,
		`src="/static/build-info.js?v=`+version+`"`,
	)
	html = strings.ReplaceAll(
		html,
		`src="/static/app.js"`,
		`src="/static/app.js?v=`+version+`"`,
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.buildInfo())
}

// handleSite はログイン前でも使える接続案内 (HTTPS URL /setup) を返す。
func (s *Server) handleSite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	body := map[string]any{
		"tlsEnabled": s.cfg.TLS.Enabled(),
		"setupUrl":   "/setup",
	}
	if s.cfg.TLS.Enabled() {
		port, err := s.cfg.TLSListenPort()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorBody("サーバ設定に問題があります"))
			return
		}
		body["httpsUrl"] = "https://" + net.JoinHostPort(s.cfg.TLS.PublicHost, port) + "/"
	}
	writeJSON(w, http.StatusOK, body)
}

type buildInfo struct {
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
	BuiltAt string `json:"builtAt,omitempty"`
}

func (info buildInfo) cacheVersion() string {
	version := info.Commit
	if version == "" {
		version = "unknown"
	}
	if info.Dirty {
		version += "-dirty"
	}
	if info.BuiltAt != "" {
		version += "-" + info.BuiltAt
	}
	return version
}

func (s *Server) buildInfo() buildInfo {
	path := filepath.Join(s.cfg.StaticDir(), "build-info.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return buildInfo{Commit: "unknown"}
	}
	var info buildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return buildInfo{Commit: "unknown"}
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	return info
}

func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := filepath.Base(r.URL.Path)
		ext := filepath.Ext(base)
		// 画面資産と ES module は版が変わっても古いキャッシュを掴み続けない。
		if ext == ".js" || ext == ".css" || base == "index.html" || base == "build-info.json" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write json: %v", err)
	}
}

func errorBody(message string) map[string]string {
	return map[string]string{"error": message}
}

func writeClientError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" || r.URL.Path == "/ws/terminal" {
		writeJSON(w, status, errorBody(message))
		return
	}
	http.Error(w, message, status)
}
