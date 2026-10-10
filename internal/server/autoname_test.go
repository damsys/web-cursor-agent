package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/terminal"
)

func TestAutoRenameMaxAgentLaunches(t *testing.T) {
	if autoRenameMaxAgentLaunches != 3 {
		t.Fatalf("autoRenameMaxAgentLaunches = %d, want 3", autoRenameMaxAgentLaunches)
	}
}

func TestRenameThreshold(t *testing.T) {
	cases := map[int]bool{
		0: false, 1: false, 2: false, 3: true,
		9: false, 10: true, 11: false,
		19: false, 20: true, 21: false,
		29: false, 30: true, 40: true,
	}
	for turns, want := range cases {
		if got := RenameThreshold(turns); got != want {
			t.Fatalf("RenameThreshold(%d) = %v, want %v", turns, got, want)
		}
	}
}

func TestSanitizeSessionTitle(t *testing.T) {
	runes := make([]rune, 45)
	for i := range runes {
		runes[i] = 'あ'
	}
	long := string(runes)
	wantLong := string(runes[:40])
	cases := []struct {
		in   string
		want string
	}{
		{"  セッション概要  ", "セッション概要"},
		{"\"引用付き\"\n二行目", "引用付き"},
		{"/rename 既にコマンド", "既にコマンド"},
		{"", ""},
		{long, wantLong},
	}
	for _, tc := range cases {
		if got := SanitizeSessionTitle(tc.in); got != tc.want {
			t.Fatalf("SanitizeSessionTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAutoRenameInjectsOnIdle(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "work")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		UsersFile:    filepath.Join(dir, "users.yaml"),
		StateDir:     filepath.Join(dir, "var"),
		AgentCommand: "agent",
		DetachGrace:  time.Minute,
		AutoRename:   config.AutoRename{Enabled: true},
		Projects: []config.Project{
			{ID: "app", Name: "app", Path: projectPath},
		},
	}
	srv := New(cfg)
	srv.nameGenerator = func(context.Context, nameGenRequest) (string, error) {
		return "自動セッション名", nil
	}

	layout, err := cursor.LayoutFor(cfg.StateDir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	chatID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	chatDir := cursor.ChatDir(layout.DataDir, projectPath, chatID)
	if err := os.MkdirAll(chatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(projectPath)
	meta := `{"title":"Old English","createdAtMs":1,"updatedAtMs":1,"hasConversation":true,"cwd":` + string(cwd) + `}`
	if err := os.WriteFile(filepath.Join(chatDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	history, err := json.Marshal([]string{"三", "二", "一"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatDir, "prompt_history.json"), history, 0o600); err != nil {
		t.Fatal(err)
	}

	term, err := terminal.Start("cat", nil, projectPath, os.Environ(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newRuntimeID()
	if err != nil {
		term.Close()
		t.Fatal(err)
	}
	rt := &runtimeSession{
		id:             id,
		username:       "alice",
		projectID:      "app",
		chatID:         chatID,
		term:           term,
		grace:          time.Minute,
		hub:            srv.runtimes,
		activity:       AgentActivityIdle,
		scrollback:     newByteRing(scrollbackMax),
		detachedBuffer: newByteRing(detachBufferMax),
		readerDone:     make(chan struct{}),
	}
	if !srv.runtimes.Reserve("alice") {
		term.Close()
		t.Fatal("reserve")
	}
	srv.runtimes.Track(rt)
	t.Cleanup(func() { rt.Terminate() })
	srv.startAutoRename(rt, "alice", cfg.Projects[0])

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := cursor.ReadAutonameState(layout.DataDir, projectPath, chatID)
		if err == nil && state.LastTitle == "自動セッション名" && state.LastTurn == 3 {
			return
		}
		// OSC 相当のタイトル更新を模して適用確認を完了させる。
		rt.setTitle("自動セッション名 - ✅ Ready")
		time.Sleep(50 * time.Millisecond)
	}
	state, _ := cursor.ReadAutonameState(layout.DataDir, projectPath, chatID)
	t.Fatalf("autoname did not apply: state=%#v sessionTitle=%q", state, rt.SessionTitle())
}

func TestAutoRenameDisabled(t *testing.T) {
	srv := New(config.Config{AutoRename: config.AutoRename{Enabled: false}})
	srv.startAutoRename(nil, "alice", config.Project{})
}

func TestBuildAutonamePrompt(t *testing.T) {
	prompt := buildAutonamePrompt([]string{"新しい話", "古い話"}, "Old")
	for _, part := range []string{"新しい話", "Old", "日本語"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("prompt missing %q: %q", part, prompt)
		}
	}
}
