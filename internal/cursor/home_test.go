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
	listedID := "11111111-1111-1111-1111-111111111111"
	emptyID := "22222222-2222-2222-2222-222222222222"
	subID := "33333333-3333-3333-3333-333333333333"
	hiddenID := "44444444-4444-4444-4444-444444444444"
	writeMeta := func(id, meta string) {
		t.Helper()
		dir := filepath.Join(data, "chats", WorkspaceHash(project), id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeMeta(listedID, `{"title":"例","createdAtMs":10,"updatedAtMs":20,"hasConversation":true,"cwd":"/work/app"}`)
	writeMeta(emptyID, `{"createdAtMs":11,"updatedAtMs":21,"hasConversation":false,"cwd":"/work/app"}`)
	writeMeta(subID, `{"createdAtMs":12,"updatedAtMs":22,"hasConversation":true,"isSubagent":true}`)
	writeMeta(hiddenID, `{"title":"隠す","createdAtMs":13,"updatedAtMs":23,"hasConversation":true,"cwd":"/work/app"}`)
	if err := HideChat(data, project, hiddenID); err != nil {
		t.Fatal(err)
	}

	chats, err := ListChats(data, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != listedID || chats[0].Title != "例" || chats[0].UpdatedAtMs != 20 {
		t.Fatalf("chats = %#v", chats)
	}
	for _, id := range []string{listedID, emptyID, subID, hiddenID} {
		ok, err := HasChat(data, project, id)
		if err != nil || !ok {
			t.Fatalf("has chat %s ok=%v err=%v", id, ok, err)
		}
	}
	if err := UnhideChat(data, project, hiddenID); err != nil {
		t.Fatal(err)
	}
	chats, err = ListChats(data, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("after unhide chats = %#v", chats)
	}

	known, err := ChatIDSet(data, project)
	if err != nil {
		t.Fatal(err)
	}
	// 空セッションも検出対象。サブエージェントは除く。
	if len(known) != 3 {
		t.Fatalf("known = %#v", known)
	}
	if _, ok := known[subID]; ok {
		t.Fatal("subagent should be excluded from known set")
	}
	newID := "55555555-5555-5555-5555-555555555555"
	writeMeta(newID, `{"createdAtMs":99,"updatedAtMs":99,"hasConversation":false,"cwd":"/work/app"}`)
	found, ok, err := FindNewChat(data, project, known)
	if err != nil || !ok || found.ID != newID {
		t.Fatalf("FindNewChat = %#v ok=%v err=%v", found, ok, err)
	}
}

func TestLayoutRejectsInvalidUsername(t *testing.T) {
	if _, err := LayoutFor(t.TempDir(), "../alice"); err == nil {
		t.Fatal("expected invalid username")
	}
}
