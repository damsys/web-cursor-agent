package server

import (
	"encoding/json"
	"log"
	"net"
	"net/http"

	"web-cursor-agent/internal/users"
)

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
