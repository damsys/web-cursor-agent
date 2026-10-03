// Package users はユーザー名、パスワードハッシュ、許可 MAC アドレスをファイルで保持する。
package users

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"web-cursor-agent/internal/security"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

// User はログインできる 1 人分の定義である。
type User struct {
	Username     string   `yaml:"username"`
	PasswordHash string   `yaml:"password_hash"`
	MACAddresses []string `yaml:"mac_addresses"`
}

// File は users.yaml の内容である。
type File struct {
	Users []User `yaml:"users"`
}

// ValidUsername はディレクトリ名に使えるユーザー名かを返す。
func ValidUsername(name string) bool {
	return usernamePattern.MatchString(name)
}

// Load はユーザーファイルを読む。ファイルが無い場合は空の一覧を返す。
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read users: %w", err)
	}
	var file File
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return File{}, fmt.Errorf("parse users: %w", err)
	}
	for i := range file.Users {
		normalized, err := security.NormalizeMACList(file.Users[i].MACAddresses)
		if err != nil {
			return File{}, fmt.Errorf("user %q: %w", file.Users[i].Username, err)
		}
		file.Users[i].MACAddresses = normalized
	}
	return file, nil
}

// Save は一時ファイルへ書いてから置き換え、権限を所有者だけに制限する。
func Save(path string, file File) error {
	raw, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode users: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create users directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".users-*.yaml")
	if err != nil {
		return fmt.Errorf("create users temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod users temp file: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("write users temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close users temp file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace users file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod users file: %w", err)
	}
	return nil
}

// Find はユーザー名に一致する定義を返す。
func (f File) Find(username string) (User, bool) {
	for _, user := range f.Users {
		if user.Username == username {
			return user, true
		}
	}
	return User{}, false
}

// Authenticate はパスワードが一致するユーザーを返す。
func (f File) Authenticate(username, password string) (User, bool) {
	user, ok := f.Find(username)
	if !ok {
		return User{}, false
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return User{}, false
	}
	return user, true
}

// AllowsMAC は正規化済みの MAC アドレスが許可リストに含まれるかを返す。
func (u User) AllowsMAC(mac string) bool {
	for _, allowed := range u.MACAddresses {
		if allowed == mac {
			return true
		}
	}
	return false
}

// Upsert はパスワードと MAC アドレスを追加または更新する。
// keepMACs が真のときは、既存ユーザーの MAC アドレスを維持する。
func (f *File) Upsert(username, password string, macs []string, keepMACs bool, cost int) error {
	if !ValidUsername(username) {
		return fmt.Errorf("invalid username %q", username)
	}
	if password == "" {
		return errors.New("password is empty")
	}
	normalized, err := security.NormalizeMACList(macs)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	for i := range f.Users {
		if f.Users[i].Username != username {
			continue
		}
		f.Users[i].PasswordHash = string(hash)
		if !keepMACs {
			f.Users[i].MACAddresses = normalized
		}
		return nil
	}
	f.Users = append(f.Users, User{
		Username:     username,
		PasswordHash: string(hash),
		MACAddresses: normalized,
	})
	return nil
}
