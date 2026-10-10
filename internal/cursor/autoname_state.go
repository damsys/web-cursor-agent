package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// autonameStateName は自動リネーム進捗を残すサイドカー。agent 所有の meta.json は触らない。
const autonameStateName = ".wca-autoname.json"

// AutonameState は自動リネームの適用状況である。
type AutonameState struct {
	LastTurn  int    `json:"last_turn"`
	LastTitle string `json:"last_title"`
	// Skip は手動リネームを検出したあと自動更新を止める。
	Skip bool `json:"skip"`
	// ReservedTurn は命名ジョブ開始時点のターン。失敗しても同ターンで agent -p を繰り返さない。
	ReservedTurn int `json:"reserved_turn,omitempty"`
	// AgentLaunches は命名用 agent -p の起動回数。チャット単位のハード上限に使う。
	AgentLaunches int `json:"agent_launches,omitempty"`
}

// ReadAutonameState はサイドカーを読む。無ければ零値を返す。
func ReadAutonameState(dataDir, projectPath, chatID string) (AutonameState, error) {
	path := filepath.Join(chatDir(dataDir, projectPath, chatID), autonameStateName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return AutonameState{}, nil
	}
	if err != nil {
		return AutonameState{}, fmt.Errorf("read autoname state: %w", err)
	}
	var state AutonameState
	if err := json.Unmarshal(raw, &state); err != nil {
		return AutonameState{}, fmt.Errorf("parse autoname state: %w", err)
	}
	return state, nil
}

// WriteAutonameState はサイドカーを原子的に書き込む。
func WriteAutonameState(dataDir, projectPath, chatID string, state AutonameState) error {
	dir := chatDir(dataDir, projectPath, chatID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create chat dir: %w", err)
	}
	path := filepath.Join(dir, autonameStateName)
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode autoname state: %w", err)
	}
	out = append(out, '\n')
	temp, err := os.CreateTemp(dir, ".wca-autoname-*.json")
	if err != nil {
		return fmt.Errorf("create temp autoname state: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temp autoname state: %w", err)
	}
	if _, err := temp.Write(out); err != nil {
		temp.Close()
		return fmt.Errorf("write temp autoname state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp autoname state: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace autoname state: %w", err)
	}
	return nil
}

// ChatTitle は meta.json の title を返す。無いときは空文字。
func ChatTitle(dataDir, projectPath, chatID string) (string, error) {
	meta, ok := readChatMeta(filepath.Join(chatDir(dataDir, projectPath, chatID), "meta.json"), projectPath)
	if !ok {
		return "", nil
	}
	return meta.Title, nil
}
