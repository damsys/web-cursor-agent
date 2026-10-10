package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPromptHistoryAndTurnCount(t *testing.T) {
	state := t.TempDir()
	layout, err := LayoutFor(state, "alice")
	if err != nil {
		t.Fatal(err)
	}
	project := "/home/alice/app"
	chatID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dir := ChatDir(layout.DataDir, project, chatID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`["新しい","古い"]`)
	if err := os.WriteFile(filepath.Join(dir, promptHistoryName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	prompts, err := ReadPromptHistory(layout.DataDir, project, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || prompts[0] != "新しい" {
		t.Fatalf("prompts = %#v", prompts)
	}
	n, err := PromptTurnCount(layout.DataDir, project, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("turns = %d", n)
	}
	missing, err := ReadPromptHistory(layout.DataDir, project, "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestAutonameStateRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	layout, err := LayoutFor(stateDir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	project := "/home/alice/app"
	chatID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if err := os.MkdirAll(ChatDir(layout.DataDir, project, chatID), 0o700); err != nil {
		t.Fatal(err)
	}
	want := AutonameState{LastTurn: 3, LastTitle: "題名", Skip: false}
	if err := WriteAutonameState(layout.DataDir, project, chatID, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAutonameState(layout.DataDir, project, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
