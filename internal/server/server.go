// Package server はログイン、プロジェクト一覧、仮想端末の中継を提供する。
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	sessionTTL    = 30 * 24 * time.Hour
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
	// scheduleRestart はテストで差し替え可能。nil のときは systemd-run による実再起動。
	scheduleRestart func() error
	sessions        *sessionStore
	runtimes        *runtimeHub
	limiter         *loginLimiter

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
	mux.HandleFunc("GET /api/build", s.handleBuild)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/projects/{id}/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/projects/{id}/sessions/{chatId}/hide", s.handleHideSession)
	mux.HandleFunc("POST /api/maintenance/restart", s.handleMaintenanceRestart)
	mux.HandleFunc("GET /ws/terminal", s.handleTerminal)
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
		switch filepath.Base(r.URL.Path) {
		case "app.js", "app.css", "build-info.js", "build-info.json", "index.html":
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
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

func (s *Server) handleHideSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	chatID := r.PathValue("chatId")
	if !chatIDPattern.MatchString(chatID) {
		writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
		return
	}
	layout, err := s.userLayout(user.Username)
	if err != nil {
		log.Printf("cursor layout: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッションを非表示にできません"))
		return
	}
	if err := cursor.HideChat(layout.DataDir, project.Path, chatID); err != nil {
		if errors.Is(err, cursor.ErrChatNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
			return
		}
		log.Printf("hide chat: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッションを非表示にできません"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	attachID := r.URL.Query().Get("attach")
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

	var rt *runtimeSession
	switch {
	case attachID != "":
		existing, found := s.runtimes.Lookup(attachID, user.Username)
		if !found || existing.projectID != project.ID {
			writeJSON(w, http.StatusNotFound, errorBody("再接続できるセッションがありません"))
			return
		}
		rt = existing
	case chatID != "":
		if existing, found := s.runtimes.FindByChat(user.Username, project.ID, chatID); found {
			rt = existing
		}
	}
	// 新規起動前に既存 ID を覚え、起動後に現れたチャットを URL 用に紐づける。
	var knownChats map[string]struct{}
	if rt == nil && chatID == "" {
		if layout, err := s.userLayout(user.Username); err == nil {
			knownChats, _ = cursor.ChatIDSet(layout.DataDir, project.Path)
		}
	}
	started := false
	if rt == nil {
		if !s.runtimes.Reserve(user.Username) {
			writeJSON(w, http.StatusTooManyRequests, errorBody("同時に開けるセッション数の上限に達しています"))
			return
		}
		session, err := s.launch(user.Username, project, chatID, 80, 24)
		if err != nil {
			s.runtimes.ReleaseReservation(user.Username)
			log.Printf("start agent user=%s project=%s: %v", user.Username, project.ID, err)
			writeJSON(w, http.StatusInternalServerError, errorBody("エージェントを起動できません"))
			return
		}
		id, err := newRuntimeID()
		if err != nil {
			session.Close()
			s.runtimes.ReleaseReservation(user.Username)
			log.Printf("runtime id: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("エージェントを起動できません"))
			return
		}
		rt = &runtimeSession{
			id:             id,
			username:       user.Username,
			projectID:      project.ID,
			chatID:         chatID,
			term:           session,
			grace:          s.cfg.DetachGrace,
			hub:            s.runtimes,
			scrollback:     newByteRing(scrollbackMax),
			detachedBuffer: newByteRing(detachBufferMax),
			readerDone:     make(chan struct{}),
		}
		s.runtimes.Track(rt)
		started = true
		log.Printf("agent started user=%s project=%s chat=%s runtime=%s", user.Username, project.ID, chatID, rt.id)
		if chatID == "" {
			s.watchNewChat(rt, user.Username, project, knownChats)
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		if started {
			rt.Terminate()
		}
		log.Printf("accept websocket: %v", err)
		return
	}
	conn.SetReadLimit(maxInputBytes + 1024)
	// ページ再読込など空の端末向け。同一画面の再接続では付けず、差分だけを受け取る。
	replay := r.URL.Query().Get("replay") == "1"
	intentionalClose, agentExited := s.relay(r.Context(), conn, rt, replay)
	if agentExited {
		log.Printf("agent stopped user=%s project=%s chat=%s runtime=%s code=%d", user.Username, project.ID, chatID, rt.id, rt.ExitCode())
		return
	}
	if intentionalClose && rt.ShouldTerminateOnClose() {
		log.Printf("agent closed on idle leave user=%s project=%s runtime=%s", user.Username, project.ID, rt.id)
		rt.Terminate()
		return
	}
	rt.DetachOutput()
	log.Printf("agent detached user=%s project=%s runtime=%s intentional=%v activity=%v", user.Username, project.ID, rt.id, intentionalClose, rt.Activity())
}

func (s *Server) relay(ctx context.Context, conn *websocket.Conn, rt *runtimeSession, replay bool) (intentionalClose bool, agentExited bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")

	buffered, live, statusLive, ok := rt.AttachOutput(replay)
	if !ok {
		payload, _ := json.Marshal(map[string]any{"type": "exit", "code": rt.ExitCode()})
		writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
		_ = conn.Write(writeCtx, websocket.MessageText, payload)
		writeCancel()
		return false, true
	}

	var (
		writeMu sync.Mutex
		exited  bool
		exitMu  sync.Mutex
	)
	setExited := func() {
		exitMu.Lock()
		exited = true
		exitMu.Unlock()
	}
	write := func(typ websocket.MessageType, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, typ, data)
	}
	helloBody := map[string]any{
		"type":     "hello",
		"attach":   rt.id,
		"chat":     rt.ChatID(),
		"activity": ActivityName(rt.Activity()),
	}
	if title := rt.SessionTitle(); title != "" {
		helloBody["title"] = title
	}
	hello, _ := json.Marshal(helloBody)
	if err := write(websocket.MessageText, hello); err != nil {
		return false, false
	}
	if len(buffered) > 0 {
		if err := write(websocket.MessageBinary, buffered); err != nil {
			return false, false
		}
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
		for {
			select {
			case <-ctx.Done():
				return
			case state, open := <-statusLive:
				if !open {
					statusLive = nil
					continue
				}
				payloadBody := map[string]any{
					"type":  "activity",
					"state": ActivityName(state.Activity),
				}
				if state.Title != "" {
					payloadBody["title"] = state.Title
				}
				if chat := state.ChatID; chat != "" {
					payloadBody["chat"] = chat
				} else if chat := rt.ChatID(); chat != "" {
					payloadBody["chat"] = chat
				}
				payload, _ := json.Marshal(payloadBody)
				if writeErr := write(websocket.MessageText, payload); writeErr != nil {
					cancel()
					return
				}
			case payload, open := <-live:
				if !open {
					// detach/reattach でも sink は閉じる。プロセス終了時だけ exit を送る。
					select {
					case <-rt.Done():
					case <-time.After(200 * time.Millisecond):
					}
					if code := rt.ExitCode(); code >= 0 {
						setExited()
						exitPayload, _ := json.Marshal(map[string]any{"type": "exit", "code": code})
						_ = write(websocket.MessageText, exitPayload)
					}
					cancel()
					return
				}
				if writeErr := write(websocket.MessageBinary, payload); writeErr != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			exitMu.Lock()
			agentExited = exited
			exitMu.Unlock()
			return intentionalClose, agentExited
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
			if _, err := rt.Write([]byte(message.Data)); err != nil {
				log.Printf("write pty: %v", err)
				exitMu.Lock()
				agentExited = exited
				exitMu.Unlock()
				return intentionalClose, agentExited
			}
		case "resize":
			if err := rt.Resize(message.Cols, message.Rows); err != nil {
				log.Printf("resize pty: %v", err)
			}
		case "close":
			return true, false
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

// watchNewChat は agent が作ったチャット履歴 ID を検出し、runtime とクライアントへ渡す。
func (s *Server) watchNewChat(rt *runtimeSession, username string, project config.Project, known map[string]struct{}) {
	if known == nil {
		known = map[string]struct{}{}
	}
	go func() {
		layout, err := s.userLayout(username)
		if err != nil {
			return
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-rt.Done():
				return
			case <-ticker.C:
				chat, found, err := cursor.FindNewChat(layout.DataDir, project.Path, known)
				if err != nil || !found {
					continue
				}
				if s.runtimes.BindChat(rt, chat.ID) {
					log.Printf("chat bound user=%s project=%s chat=%s runtime=%s", username, project.ID, chat.ID, rt.id)
					return
				}
				// 他 runtime が先に取った ID は known に足して次を探す。
				known[chat.ID] = struct{}{}
			}
		}
	}()
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

type persistedSession struct {
	Username      string `json:"username"`
	ExpiresUnixMS int64  `json:"expires_unix_ms"`
}

type persistedSessions struct {
	Sessions map[string]persistedSession `json:"sessions"`
}

type sessionStore struct {
	mu       sync.Mutex
	path     string
	sessions map[string]webSession
}

// newSessionStore はファイルから既存セッションを読み込み、再起動後もログインを維持する。
func newSessionStore(path string) *sessionStore {
	store := &sessionStore{
		path:     path,
		sessions: map[string]webSession{},
	}
	if err := store.load(); err != nil {
		log.Printf("load sessions: %v", err)
	}
	return store
}

func (s *sessionStore) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file persistedSessions
	if err := json.Unmarshal(raw, &file); err != nil {
		return err
	}
	now := time.Now()
	for token, entry := range file.Sessions {
		expires := time.UnixMilli(entry.ExpiresUnixMS)
		if now.After(expires) || entry.Username == "" {
			continue
		}
		s.sessions[token] = webSession{Username: entry.Username, Expires: expires}
	}
	return nil
}

// persistLocked はメモリ上のセッションを所有者専用の JSON へ原子的に書き出す。
func (s *sessionStore) persistLocked() error {
	file := persistedSessions{Sessions: make(map[string]persistedSession, len(s.sessions))}
	for token, session := range s.sessions {
		file.Sessions[token] = persistedSession{
			Username:      session.Username,
			ExpiresUnixMS: session.Expires.UnixMilli(),
		}
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".sessions-*.json")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

// Create はログイン成功後のセッショントークンを発行する。
func (s *sessionStore) Create(username string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = webSession{Username: username, Expires: time.Now().Add(sessionTTL)}
	if err := s.persistLocked(); err != nil {
		delete(s.sessions, token)
		return "", err
	}
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
		if err := s.persistLocked(); err != nil {
			log.Printf("persist sessions: %v", err)
		}
		return "", false
	}
	return session.Username, true
}

// Delete はログアウトしたトークンを破棄する。
func (s *sessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	if err := s.persistLocked(); err != nil {
		log.Printf("persist sessions: %v", err)
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
