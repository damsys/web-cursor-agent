package cursor

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutForAndEnviron(t *testing.T) {
	state := t.TempDir()
	layout, err := LayoutFor(state, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.XDGConfigHome, "api_key"), []byte(" secret \n"), 0o600); err != nil {
		if err := os.MkdirAll(layout.XDGConfigHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(layout.XDGConfigHome, "api_key"), []byte(" secret \n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if layout.APIKey != "secret" {
		t.Fatalf("api key = %q", layout.APIKey)
	}
	env := layout.Environ([]string{
		"PATH=/usr/bin",
		"HOME=/home/alice",
		"CURSOR_API_KEY=parent",
		"CURSOR_SANDBOX=native",
		"HTTP_PROXY=http://127.0.0.1:9",
		"GIT_SSH_COMMAND=cursorsandbox",
		"LANG=ja_JP.UTF-8",
	})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "parent") || strings.Contains(joined, "CURSOR_SANDBOX") || strings.Contains(joined, "HTTP_PROXY") || strings.Contains(joined, "cursorsandbox") {
		t.Fatalf("env leaked sandbox or parent key:\n%s", joined)
	}
	if !strings.Contains(joined, "CURSOR_API_KEY=secret") {
		t.Fatalf("missing user api key:\n%s", joined)
	}
	if !strings.Contains(joined, "CURSOR_DATA_DIR="+layout.DataDir) {
		t.Fatalf("missing data dir:\n%s", joined)
	}
	if !strings.Contains(joined, "LANG=ja_JP.UTF-8") {
		t.Fatal("existing LANG should be kept")
	}
}

func TestWorkspaceHashAndChats(t *testing.T) {
	// agent は絶対パス文字列の MD5 を履歴ディレクトリ名にする。実行環境のパスには依存させない。
	project := "/work/app"
	sum := md5.Sum([]byte(project))
	if got := WorkspaceHash(project); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash = %s", got)
	}
	data := t.TempDir()
	chatDir := filepath.Join(data, "chats", WorkspaceHash(project), "11111111-1111-1111-1111-111111111111")
	if err := os.MkdirAll(chatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"title":"例","createdAtMs":10,"updatedAtMs":20,"hasConversation":true,"cwd":"/work/app"}`
	if err := os.WriteFile(filepath.Join(chatDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	chats, err := ListChats(data, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Title != "例" || chats[0].UpdatedAtMs != 20 {
		t.Fatalf("chats = %#v", chats)
	}
	ok, err := HasChat(data, project, chats[0].ID)
	if err != nil || !ok {
		t.Fatalf("has chat ok=%v err=%v", ok, err)
	}
}

func TestLayoutRejectsInvalidUsername(t *testing.T) {
	if _, err := LayoutFor(t.TempDir(), "../alice"); err == nil {
		t.Fatal("expected invalid username")
	}
}
