// Package server はログイン、プロジェクト一覧、仮想端末の中継を提供する。
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/security"
	"web-cursor-agent/internal/terminal"
	"web-cursor-agent/internal/users"
)

const (
	cookieName    = "wca_session"
	sessionTTL    = 24 * time.Hour
	maxSessions   = 4
	maxInputBytes = 256 * 1024
	loginFailures = 8
	loginWindow   = 5 * time.Minute
)

var chatIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// LaunchFunc はユーザーの認証ディレクトリで対話プロセスを起動する。
type LaunchFunc func(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error)

// Server は HTTP と WebSocket の入口である。
type Server struct {
	cfg       config.Config
	allowsIP  func(net.IP) bool
	lookupMAC func(net.IP) (string, bool)
	remoteIP  func(*http.Request) net.IP
	launch    LaunchFunc
	sessions  *sessionStore
	terms     *termTracker
	limiter   *loginLimiter
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
		sessions:  newSessionStore(),
		terms:     newTermTracker(maxSessions),
		limiter:   newLoginLimiter(loginFailures, loginWindow),
	}
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
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/projects/{id}/sessions", s.handleSessions)
	mux.HandleFunc("GET /ws/terminal", s.handleTerminal)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.cfg.StaticDir()))))
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
	s.terms.CloseAll()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.cfg.StaticDir()+"/index.html")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.remoteIP(r)
	if !s.limiter.Allow(ip.String()) {
		writeJSON(w, http.StatusTooManyRequests, errorBody("試行回数が多すぎます。しばらく待ってから再度お試しください"))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("入力内容を読み取れません"))
		return
	}
	file, err := users.Load(s.cfg.UsersFile)
	if err != nil {
		log.Printf("load users: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("ユーザー情報を読み取れません"))
		return
	}
	user, ok := file.Authenticate(body.Username, body.Password)
	if !ok {
		s.limiter.Fail(ip.String())
		log.Printf("login failed user=%s ip=%s", body.Username, ip)
		writeJSON(w, http.StatusUnauthorized, errorBody("ユーザー名またはパスワードが正しくありません"))
		return
	}
	if message, status := s.authorizeMAC(user, ip); status != 0 {
		log.Printf("mac check failed user=%s ip=%s", user.Username, ip)
		writeJSON(w, status, errorBody(message))
		return
	}
	s.limiter.Reset(ip.String())
	token, err := s.sessions.Create(user.Username)
	if err != nil {
		log.Printf("create session: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("ログイン状態を保存できません"))
		return
	}
	http.SetCookie(w, sessionCookie(token, r.TLS != nil))
	log.Printf("login succeeded user=%s ip=%s", user.Username, ip)
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(w, r); !ok {
		return
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	items := make([]item, 0, len(s.cfg.Projects))
	for _, project := range s.cfg.Projects {
		items = append(items, item{ID: project.ID, Name: project.Name, Path: project.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	layout, err := s.userLayout(user.Username)
	if err != nil {
		log.Printf("cursor layout: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッション一覧を読み取れません"))
		return
	}
	chats, err := cursor.ListChats(layout.DataDir, project.Path)
	if err != nil {
		log.Printf("list chats: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッション一覧を読み取れません"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": chats})
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.URL.Query().Get("project"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	chatID := r.URL.Query().Get("chat")
	if chatID != "" {
		if !chatIDPattern.MatchString(chatID) {
			writeJSON(w, http.StatusBadRequest, errorBody("セッションが見つかりません"))
			return
		}
		layout, err := s.userLayout(user.Username)
		if err != nil {
			log.Printf("cursor layout: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("セッションを開始できません"))
			return
		}
		found, err := cursor.HasChat(layout.DataDir, project.Path, chatID)
		if err != nil {
			log.Printf("lookup chat: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("セッションを開始できません"))
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
			return
		}
	}
	if !s.terms.Reserve(user.Username) {
		writeJSON(w, http.StatusTooManyRequests, errorBody("同時に開けるセッション数の上限に達しています"))
		return
	}
	session, err := s.launch(user.Username, project, chatID, 80, 24)
	if err != nil {
		s.terms.Release(user.Username, nil)
		log.Printf("start agent user=%s project=%s: %v", user.Username, project.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody("エージェントを起動できません"))
		return
	}
	s.terms.Track(user.Username, session)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		session.Close()
		s.terms.Release(user.Username, session)
		log.Printf("accept websocket: %v", err)
		return
	}
	conn.SetReadLimit(maxInputBytes + 1024)
	log.Printf("agent started user=%s project=%s chat=%s", user.Username, project.ID, chatID)
	s.relay(r.Context(), conn, session)
	session.Close()
	s.terms.Release(user.Username, session)
	log.Printf("agent stopped user=%s project=%s chat=%s code=%d", user.Username, project.ID, chatID, session.ExitCode())
}

func (s *Server) relay(ctx context.Context, conn *websocket.Conn, session *terminal.Session) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")
	var writeMu sync.Mutex
	write := func(typ websocket.MessageType, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, typ, data)
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				payload := append([]byte(nil), buf[:n]...)
				if writeErr := write(websocket.MessageBinary, payload); writeErr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
					log.Printf("read pty: %v", err)
				}
				code := session.ExitCode()
				if code < 0 {
					select {
					case <-session.Done():
						code = session.ExitCode()
					case <-time.After(time.Second):
						code = 1
					}
				}
				payload, _ := json.Marshal(map[string]any{"type": "exit", "code": code})
				_ = write(websocket.MessageText, payload)
				cancel()
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var message struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		switch message.Type {
		case "input":
			if len(message.Data) > maxInputBytes {
				continue
			}
			if _, err := session.Write([]byte(message.Data)); err != nil {
				log.Printf("write pty: %v", err)
				return
			}
		case "resize":
			if err := session.Resize(message.Cols, message.Rows); err != nil {
				log.Printf("resize pty: %v", err)
			}
		}
	}
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (users.User, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorBody("ログインが必要です"))
		return users.User{}, false
	}
	username, ok := s.sessions.Lookup(cookie.Value)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("ログインが必要です"))
		return users.User{}, false
	}
	file, err := users.Load(s.cfg.UsersFile)
	if err != nil {
		log.Printf("load users: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("ユーザー情報を読み取れません"))
		return users.User{}, false
	}
	user, ok := file.Find(username)
	if !ok {
		s.sessions.Delete(cookie.Value)
		writeJSON(w, http.StatusUnauthorized, errorBody("ログインが必要です"))
		return users.User{}, false
	}
	if message, status := s.authorizeMAC(user, s.remoteIP(r)); status != 0 {
		writeJSON(w, status, errorBody(message))
		return users.User{}, false
	}
	return user, true
}

func (s *Server) authorizeMAC(user users.User, ip net.IP) (string, int) {
	if !s.cfg.MacCheck || ip == nil || ip.IsLoopback() {
		return "", 0
	}
	if len(user.MACAddresses) == 0 {
		return "MAC アドレスが未登録です", http.StatusForbidden
	}
	mac, ok := s.lookupMAC(ip)
	if !ok {
		return "この端末の MAC アドレスを確認できません", http.StatusForbidden
	}
	if !user.AllowsMAC(mac) {
		return "この端末からのアクセスは許可されていません", http.StatusForbidden
	}
	return "", 0
}

func (s *Server) userLayout(username string) (cursor.Layout, error) {
	layout, err := cursor.LayoutFor(s.cfg.StateDir, username)
	if err != nil {
		return cursor.Layout{}, err
	}
	if err := layout.Ensure(); err != nil {
		return cursor.Layout{}, err
	}
	return layout, nil
}

func launchAgent(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error) {
	return nil, errors.New("agent launch is not configured")
}

// BindLaunch は設定から agent の起動処理を組み立ててサーバへ設定する。
func (s *Server) BindLaunch() {
	cfg := s.cfg
	s.launch = func(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error) {
		layout, err := cursor.LayoutFor(cfg.StateDir, username)
		if err != nil {
			return nil, err
		}
		if err := layout.Ensure(); err != nil {
			return nil, err
		}
		command := cfg.AgentCommand
		if _, err := exec.LookPath(command); err != nil && !hasSlash(command) {
			return nil, err
		}
		args := []string{"--workspace", project.Path, "--trust"}
		if chatID != "" {
			args = append([]string{"--resume", chatID}, args...)
		}
		return terminal.Start(command, args, project.Path, layout.Environ(os.Environ()), cols, rows)
	}
}

func hasSlash(path string) bool {
	return len(path) > 0 && (path[0] == '/' || path[0] == '.')
}

func sessionCookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	}
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

type webSession struct {
	Username string
	Expires  time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]webSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[string]webSession{}}
}

// Create はログイン成功後のセッショントークンを発行する。
func (s *sessionStore) Create(username string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	s.sessions[token] = webSession{Username: username, Expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	return token, nil
}

// Lookup は期限内のトークンからユーザー名を返す。
func (s *sessionStore) Lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(session.Expires) {
		delete(s.sessions, token)
		return "", false
	}
	return session.Username, true
}

// Delete はログアウトしたトークンを破棄する。
func (s *sessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

type termTracker struct {
	mu       sync.Mutex
	counts   map[string]int
	sessions map[*terminal.Session]string
	max      int
}

func newTermTracker(max int) *termTracker {
	return &termTracker{
		counts:   map[string]int{},
		sessions: map[*terminal.Session]string{},
		max:      max,
	}
}

// Reserve はユーザーの同時セッション数に空きがあるとき枠を確保する。
func (t *termTracker) Reserve(username string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.counts[username] >= t.max {
		return false
	}
	t.counts[username]++
	return true
}

// Track は停止時にまとめて終了できるよう、起動した対話プロセスを覚える。
func (t *termTracker) Track(username string, session *terminal.Session) {
	t.mu.Lock()
	t.sessions[session] = username
	t.mu.Unlock()
}

// Release は確保したセッション枠を返す。
func (t *termTracker) Release(username string, session *terminal.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if session != nil {
		delete(t.sessions, session)
	}
	if t.counts[username] > 0 {
		t.counts[username]--
	}
}

// CloseAll はサーバ停止時に、残っている対話プロセスを終了する。
func (t *termTracker) CloseAll() {
	t.mu.Lock()
	sessions := make([]*terminal.Session, 0, len(t.sessions))
	for session := range t.sessions {
		sessions = append(sessions, session)
	}
	t.sessions = map[*terminal.Session]string{}
	t.counts = map[string]int{}
	t.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, fails: map[string][]time.Time{}}
}

// Allow は直近の失敗回数が上限未満のときにログイン試行を許可する。
func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = recent(l.fails[key], l.window)
	return len(l.fails[key]) < l.max
}

// Fail は失敗したログイン試行を記録する。
func (l *loginLimiter) Fail(key string) {
	l.mu.Lock()
	l.fails[key] = append(recent(l.fails[key], l.window), time.Now())
	l.mu.Unlock()
}

// Reset はログイン成功後に失敗回数を消す。
func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}

func recent(times []time.Time, window time.Duration) []time.Time {
	cutoff := time.Now().Add(-window)
	kept := times[:0]
	for _, ts := range times {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	return kept
}
