package server

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

const serviceUnit = "web-cursor-agent.service"

// scheduleServiceRestart は systemd ユーザマネージャ経由で遅延再起動を予約する。
// 自プロセスが止まるため、API 応答後に切り離して実行する。
func (s *Server) scheduleServiceRestart() error {
	script := fmt.Sprintf("sleep 2; systemctl --user restart %s", serviceUnit)
	cmd := exec.Command("systemd-run", "--user", "--collect", "/bin/bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("systemd-run: %w", err)
		}
		return fmt.Errorf("systemd-run: %w: %s", err, detail)
	}
	return nil
}

func (s *Server) handleMaintenanceRestart(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}

	s.restartMu.Lock()
	if s.restartPending {
		s.restartMu.Unlock()
		writeJSON(w, http.StatusConflict, errorBody("再起動はすでに受け付けています"))
		return
	}
	s.restartPending = true
	s.restartMu.Unlock()

	schedule := s.scheduleRestart
	if schedule == nil {
		schedule = s.scheduleServiceRestart
	}
	if err := schedule(); err != nil {
		s.restartMu.Lock()
		s.restartPending = false
		s.restartMu.Unlock()
		log.Printf("schedule restart user=%s: %v", user.Username, err)
		writeJSON(w, http.StatusInternalServerError, errorBody("サービスの再起動を予約できません"))
		return
	}

	log.Printf("service restart scheduled user=%s", user.Username)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"message": "サービスの再起動を予約しました",
	})
}
