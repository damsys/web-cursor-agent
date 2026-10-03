package users

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestUpsertAndAuthenticate(t *testing.T) {
	var file File
	if err := file.Upsert("alice", "secret", []string{"AA-BB-CC-DD-EE-FF"}, false, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	if err := file.Upsert("alice", "next", nil, true, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	user, ok := file.Authenticate("alice", "next")
	if !ok {
		t.Fatal("expected authentication to succeed")
	}
	if len(user.MACAddresses) != 1 || user.MACAddresses[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("macs = %#v", user.MACAddresses)
	}
	if _, ok := file.Authenticate("alice", "secret"); ok {
		t.Fatal("old password should fail")
	}
	if _, ok := file.Authenticate("bob", "next"); ok {
		t.Fatal("unknown user should fail")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.yaml")
	var file File
	if err := file.Upsert("alice", "secret", []string{"aa:bb:cc:dd:ee:ff"}, false, bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, file); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Authenticate("alice", "secret"); !ok {
		t.Fatal("loaded user should authenticate")
	}
}

func TestLoadMissingFile(t *testing.T) {
	file, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Users) != 0 {
		t.Fatalf("users = %d", len(file.Users))
	}
}
