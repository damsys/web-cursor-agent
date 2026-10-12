// session の永続化と参照を扱う。
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
