package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const promptHistoryName = "prompt_history.json"

// ChatDir は 1 チャット履歴のディレクトリを返す。
func ChatDir(dataDir, projectPath, chatID string) string {
	return chatDir(dataDir, projectPath, chatID)
}

// ReadPromptHistory はユーザー発話の一覧を返す。新しいものが先頭。
// ファイルが無ければ空切片を返す。
func ReadPromptHistory(dataDir, projectPath, chatID string) ([]string, error) {
	path := filepath.Join(chatDir(dataDir, projectPath, chatID), promptHistoryName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt history: %w", err)
	}
	var prompts []string
	if err := json.Unmarshal(raw, &prompts); err != nil {
		return nil, fmt.Errorf("parse prompt history: %w", err)
	}
	return prompts, nil
}

// PromptTurnCount はユーザー発話の件数 (ターン数) を返す。
func PromptTurnCount(dataDir, projectPath, chatID string) (int, error) {
	prompts, err := ReadPromptHistory(dataDir, projectPath, chatID)
	if err != nil {
		return 0, err
	}
	return len(prompts), nil
}
