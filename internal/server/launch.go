package server

import (
	"errors"
	"os"
	"os/exec"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/terminal"
)

func launchAgent(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error) {
	return nil, errors.New("agent launch is not configured")
}

// BindLaunch は設定から agent の起動処理を組み立ててサーバへ設定する。
func (s *Server) BindLaunch() {
	cfg := s.cfg
	s.launch = func(username string, project config.Project, chatID string, cols, rows int) (*terminal.Session, error) {
		layout, err := cursor.LayoutFor(cfg.StateDir, username)
		if err != nil {
			return nil, err
		}
		if err := layout.Ensure(); err != nil {
			return nil, err
		}
		command := cfg.AgentCommand
		if _, err := exec.LookPath(command); err != nil && !hasSlash(command) {
			return nil, err
		}
		args := []string{"--workspace", project.Path, "--trust"}
		if chatID != "" {
			args = append([]string{"--resume", chatID}, args...)
		}
		return terminal.Start(command, args, project.Path, layout.Environ(os.Environ()), cols, rows)
	}
}

func hasSlash(path string) bool {
	return len(path) > 0 && (path[0] == '/' || path[0] == '.')
}
