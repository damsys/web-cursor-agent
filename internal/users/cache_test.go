package users

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoadUsesMtimeCacheAndInvalidatesOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	var file File
	if err := file.Upsert("alice", "secret", nil, false, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, file); err != nil {
		t.Fatal(err)
	}

	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.Authenticate("alice", "secret"); !ok {
		t.Fatal("first load should authenticate")
	}
	second, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Authenticate("alice", "secret"); !ok {
		t.Fatal("cached load should authenticate")
	}

	var next File
	if err := next.Upsert("bob", "other", nil, false, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, next); err != nil {
		t.Fatal(err)
	}
	// 一部 FS は mtime 解像度が粗いので、確実に進める。
	time.Sleep(20 * time.Millisecond)
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Authenticate("bob", "other"); !ok {
		t.Fatal("after Save, Load should see new users")
	}
	if _, ok := loaded.Authenticate("alice", "secret"); ok {
		t.Fatal("old user should be gone after Save")
	}
}
