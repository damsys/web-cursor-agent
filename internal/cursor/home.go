// Package cursor はユーザーごとの Cursor CLI 状態ディレクトリとチャット履歴を扱う。
package cursor

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"web-cursor-agent/internal/users"
)

// Layout は 1 人のアプリユーザーに割り当てる Cursor のディレクトリである。
type Layout struct {
	XDGConfigHome string
	ConfigDir     string
	DataDir       string
	APIKey        string
}

// LayoutFor は認証ファイルとチャット履歴を OS ユーザー以外の単位で分けるパスを決める。
func LayoutFor(stateDir, username string) (Layout, error) {
	if !users.ValidUsername(username) {
		return Layout{}, fmt.Errorf("invalid username %q", username)
	}
	stateAbs, err := filepath.Abs(stateDir)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve state dir: %w", err)
	}
	root := filepath.Join(stateAbs, "cursor", username)
	rel, err := filepath.Rel(stateAbs, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Layout{}, fmt.Errorf("cursor home escapes state dir")
	}
	cursorDir := filepath.Join(root, "cursor")
	return Layout{
		XDGConfigHome: root,
		ConfigDir:     cursorDir,
		DataDir:       cursorDir,
	}, nil
}

// Ensure はユーザー用ディレクトリを作成し、API キーがあれば読み込む。
// あわせて CLI の status indicators を有効にし、切断時の状態判定に使えるようにする。
func (l *Layout) Ensure() error {
	if err := os.MkdirAll(l.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create cursor home: %w", err)
	}
	if err := os.Chmod(l.XDGConfigHome, 0o700); err != nil {
		return fmt.Errorf("chmod cursor home: %w", err)
	}
	if err := EnsureStatusIndicators(l.ConfigDir); err != nil {
		return err
	}
	key, err := readAPIKey(filepath.Join(l.XDGConfigHome, "api_key"))
	if err != nil {
		return err
	}
	l.APIKey = key
	return nil
}

// Environ は親プロセスの環境から、このユーザーの Cursor 状態を指す環境を作る。
// IDE サンドボックスの変数と、親の API キーは子へ引き継がない。
func (l Layout) Environ(base []string) []string {
	sandbox := false
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key == "CURSOR_SANDBOX" {
			sandbox = true
		}
	}
	values := map[string]string{}
	var order []string
	add := func(key, value string) {
		if _, ok := values[key]; !ok {
			order = append(order, key)
		}
		values[key] = value
	}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || dropEnv(key, value, sandbox) {
			continue
		}
		add(key, value)
	}
	add("XDG_CONFIG_HOME", l.XDGConfigHome)
	add("CURSOR_CONFIG_DIR", l.ConfigDir)
	add("CURSOR_DATA_DIR", l.DataDir)
	add("TERM", "xterm-256color")
	add("COLORTERM", "truecolor")
	if l.APIKey != "" {
		add("CURSOR_API_KEY", l.APIKey)
	}
	if _, ok := values["LANG"]; !ok {
		if _, ok := values["LC_ALL"]; !ok {
			add("LANG", "C.UTF-8")
		}
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+values[key])
	}
	return out
}

// Chat はプロジェクトに紐づく既存のエージェントセッションである。
type Chat struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	UpdatedAtMs     int64  `json:"updated_at_ms"`
	CreatedAtMs     int64  `json:"created_at_ms"`
	HasConversation bool   `json:"has_conversation"`
}

type chatMeta struct {
	Title           string `json:"title"`
	CreatedAtMs     int64  `json:"createdAtMs"`
	UpdatedAtMs     int64  `json:"updatedAtMs"`
	HasConversation bool   `json:"hasConversation"`
	Cwd             string `json:"cwd"`
}

// WorkspaceHash は agent がチャット履歴ディレクトリに使う、絶対パスの MD5 である。
func WorkspaceHash(projectPath string) string {
	sum := md5.Sum([]byte(projectPath))
	return hex.EncodeToString(sum[:])
}

// ListChats はプロジェクトの絶対パスに対応するチャット履歴を新しい順に返す。
func ListChats(dataDir, projectPath string) ([]Chat, error) {
	dir := filepath.Join(dataDir, "chats", WorkspaceHash(projectPath))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Chat{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chats: %w", err)
	}
	var chats []Chat
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		metaPath := filepath.Join(dir, id, "meta.json")
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta chatMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if meta.Cwd != "" && meta.Cwd != projectPath {
			continue
		}
		updated := meta.UpdatedAtMs
		if updated == 0 {
			updated = meta.CreatedAtMs
		}
		chats = append(chats, Chat{
			ID:              id,
			Title:           meta.Title,
			UpdatedAtMs:     updated,
			CreatedAtMs:     meta.CreatedAtMs,
			HasConversation: meta.HasConversation,
		})
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].UpdatedAtMs == chats[j].UpdatedAtMs {
			return chats[i].ID > chats[j].ID
		}
		return chats[i].UpdatedAtMs > chats[j].UpdatedAtMs
	})
	return chats, nil
}

// HasChat は再開対象のチャット ID がそのプロジェクトの履歴に存在するかを返す。
func HasChat(dataDir, projectPath, chatID string) (bool, error) {
	chats, err := ListChats(dataDir, projectPath)
	if err != nil {
		return false, err
	}
	for _, chat := range chats {
		if chat.ID == chatID {
			return true, nil
		}
	}
	return false, nil
}

func readAPIKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read api key: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func dropEnv(key, value string, sandbox bool) bool {
	switch key {
	case "XDG_CONFIG_HOME", "CURSOR_CONFIG_DIR", "CURSOR_DATA_DIR", "CURSOR_API_KEY",
		"CURSOR_AGENT", "CURSOR_CONVERSATION_ID", "CURSOR_REQUEST_ID", "CURSOR_LAYOUT",
		"AGENT_TRANSCRIPTS":
		return true
	}
	if strings.Contains(key, "CURSOR_SANDBOX") || strings.HasPrefix(key, "__CURSOR_SANDBOX") {
		return true
	}
	if key == "GIT_SSH_COMMAND" && strings.Contains(value, "cursorsandbox") {
		return true
	}
	if sandbox && isProxy(key) && strings.Contains(value, "127.0.0.1") {
		return true
	}
	return false
}

func isProxy(key string) bool {
	switch strings.ToLower(key) {
	case "http_proxy", "https_proxy", "all_proxy", "socks_proxy", "socks5_proxy":
		return true
	default:
		return false
	}
}
