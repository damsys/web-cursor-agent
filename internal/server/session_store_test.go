package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStorePersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	token, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	if username, ok := store.Lookup(token); !ok || username != "alice" {
		t.Fatalf("lookup = %q %v", username, ok)
	}

	reloaded := newSessionStore(path)
	if username, ok := reloaded.Lookup(token); !ok || username != "alice" {
		t.Fatalf("reloaded lookup = %q %v", username, ok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", info.Mode().Perm())
	}
}

func TestSessionStoreDeleteRemovesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	token, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	store.Delete(token)
	if _, ok := store.Lookup(token); ok {
		t.Fatal("deleted token still looks up")
	}
	reloaded := newSessionStore(path)
	if _, ok := reloaded.Lookup(token); ok {
		t.Fatal("deleted token survived reload")
	}
}

func TestSessionStoreExpiresAndPersistsCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	token, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	session := store.sessions[token]
	session.Expires = time.Now().Add(-time.Second)
	store.sessions[token] = session
	if err := store.persistLocked(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()

	if _, ok := store.Lookup(token); ok {
		t.Fatal("expired token looked up")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file persistedSessions
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if _, ok := file.Sessions[token]; ok {
		t.Fatal("expired token remained in file")
	}
}

func TestSessionStoreIgnoresCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newSessionStore(path)
	token, err := store.Create("bob")
	if err != nil {
		t.Fatal(err)
	}
	if username, ok := store.Lookup(token); !ok || username != "bob" {
		t.Fatalf("lookup = %q %v", username, ok)
	}
}
