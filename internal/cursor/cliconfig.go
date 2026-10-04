package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const cliConfigName = "cli-config.json"

// EnsureStatusIndicators は端末タイトルに busy/idle を出す設定を有効にする。
// WebSocket 切断時の即終了判定に使う。既存の他設定は維持する。
func EnsureStatusIndicators(configDir string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create cursor config dir: %w", err)
	}
	path := filepath.Join(configDir, cliConfigName)
	root := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parse cli-config.json: %w", err)
		}
	case os.IsNotExist(err):
		root["version"] = 1
	default:
		return fmt.Errorf("read cli-config.json: %w", err)
	}
	display, _ := root["display"].(map[string]any)
	if display == nil {
		display = map[string]any{}
		root["display"] = display
	}
	if enabled, ok := display["showStatusIndicators"].(bool); ok && enabled {
		return nil
	}
	display["showStatusIndicators"] = true
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cli-config.json: %w", err)
	}
	out = append(out, '\n')
	temp, err := os.CreateTemp(configDir, ".cli-config-*.json")
	if err != nil {
		return fmt.Errorf("create temp cli-config: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temp cli-config: %w", err)
	}
	if _, err := temp.Write(out); err != nil {
		temp.Close()
		return fmt.Errorf("write temp cli-config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp cli-config: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace cli-config.json: %w", err)
	}
	return os.Chmod(path, 0o600)
}
