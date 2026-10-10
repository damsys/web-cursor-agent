package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
)

const (
	autoRenamePollInterval   = time.Second
	autoRenameNameTimeout    = 90 * time.Second
	autoRenameMaxTitleRunes  = 40
	autoRenameMaxPrompts     = 12
	autoRenameMaxInjectTries = 3
	// autoRenameMaxAgentLaunches はチャットあたりの命名用 agent -p 起動上限。
	autoRenameMaxAgentLaunches = 3
	autoRenameInjectGap        = 5 * time.Second
	autoRenameEnterDelay       = 100 * time.Millisecond
	autoRenameClearDelay       = 50 * time.Millisecond
)

// RenameThreshold は自動リネームを試みるターン数かを返す。
// 3, 10, 20 と、それ以降の 10 の倍数。
func RenameThreshold(turns int) bool {
	switch turns {
	case 3, 10, 20:
		return true
	default:
		return turns > 20 && turns%10 == 0
	}
}

// SanitizeSessionTitle は /rename に渡すタイトルを整える。
func SanitizeSessionTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexAny(raw, "\r\n"); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	raw = strings.Trim(raw, `"'「」『』`)
	raw = strings.TrimPrefix(raw, "/rename")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "/")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range raw {
		if r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(out) > autoRenameMaxTitleRunes {
		runes := []rune(out)
		out = string(runes[:autoRenameMaxTitleRunes])
		out = strings.TrimSpace(out)
	}
	return out
}

type nameGenRequest struct {
	Username     string
	Project      config.Project
	Prompts      []string
	CurrentTitle string
}

// startAutoRename はチャット ID 確定後に自動リネーム監視を開始する。
func (s *Server) startAutoRename(rt *runtimeSession, username string, project config.Project) {
	if !s.cfg.AutoRename.Enabled {
		return
	}
	go s.autoRenameLoop(rt, username, project)
}

func (s *Server) autoRenameLoop(rt *runtimeSession, username string, project config.Project) {
	ticker := time.NewTicker(autoRenamePollInterval)
	defer ticker.Stop()

	var mu sync.Mutex
	generating := false
	pendingTitle := ""
	pendingTurn := 0
	injectTries := 0
	agentLaunches := 0 // この runtime 内の起動回数 (永続カウンタと併用)
	var lastInjectAt time.Time
	loggedLaunchCap := false

	for {
		select {
		case <-rt.Done():
			return
		case <-ticker.C:
			chatID := rt.ChatID()
			if chatID == "" {
				continue
			}
			layout, err := s.userLayout(username)
			if err != nil {
				continue
			}
			state, err := cursor.ReadAutonameState(layout.DataDir, project.Path, chatID)
			if err != nil {
				log.Printf("autoname state read user=%s chat=%s: %v", username, chatID, err)
				continue
			}
			if state.Skip {
				continue
			}
			mu.Lock()
			if state.AgentLaunches > agentLaunches {
				agentLaunches = state.AgentLaunches
			}
			launches := agentLaunches
			pending := pendingTitle
			pendTurn := pendingTurn
			busy := generating
			tries := injectTries
			injectedAt := lastInjectAt
			mu.Unlock()

			metaTitle, err := cursor.ChatTitle(layout.DataDir, project.Path, chatID)
			if err != nil {
				continue
			}

			// 適用待ち中は手動判定しない (meta 反映遅れで誤スキップしないため)。
			if pending == "" && !busy && state.LastTitle != "" && metaTitle != "" && metaTitle != state.LastTitle {
				state.Skip = true
				if err := cursor.WriteAutonameState(layout.DataDir, project.Path, chatID, state); err != nil {
					log.Printf("autoname skip save user=%s chat=%s: %v", username, chatID, err)
				} else {
					log.Printf("autoname skipped after manual rename user=%s chat=%s", username, chatID)
				}
				continue
			}

			if pending != "" {
				liveTitle := rt.SessionTitle()
				if liveTitle == pending || metaTitle == pending {
					state.LastTurn = pendTurn
					state.LastTitle = pending
					state.ReservedTurn = pendTurn
					if err := cursor.WriteAutonameState(layout.DataDir, project.Path, chatID, state); err != nil {
						log.Printf("autoname state write user=%s chat=%s: %v", username, chatID, err)
					} else {
						log.Printf("autoname applied user=%s chat=%s turn=%d title=%q", username, chatID, pendTurn, pending)
					}
					mu.Lock()
					pendingTitle = ""
					pendingTurn = 0
					injectTries = 0
					lastInjectAt = time.Time{}
					mu.Unlock()
					continue
				}
				if tries >= autoRenameMaxInjectTries {
					// Enter 未認識などで反映できないときは、同ターンの再生成・再送を止める。
					state.LastTurn = pendTurn
					state.ReservedTurn = pendTurn
					if err := cursor.WriteAutonameState(layout.DataDir, project.Path, chatID, state); err != nil {
						log.Printf("autoname give-up save user=%s chat=%s: %v", username, chatID, err)
					}
					log.Printf("autoname give up rename user=%s chat=%s turn=%d title=%q tries=%d", username, chatID, pendTurn, pending, tries)
					mu.Lock()
					pendingTitle = ""
					pendingTurn = 0
					injectTries = 0
					lastInjectAt = time.Time{}
					mu.Unlock()
					continue
				}
				canInject := rt.Activity() == AgentActivityIdle &&
					(tries == 0 || time.Since(injectedAt) >= autoRenameInjectGap)
				if canInject {
					if err := injectRename(rt, pending); err != nil {
						log.Printf("autoname rename inject user=%s chat=%s: %v", username, chatID, err)
					} else {
						mu.Lock()
						injectTries++
						lastInjectAt = time.Now()
						tryN := injectTries
						mu.Unlock()
						log.Printf("autoname rename sent user=%s chat=%s turn=%d title=%q try=%d", username, chatID, pendTurn, pending, tryN)
					}
				}
				continue
			}

			// 命名用 agent -p のハード上限 (チャット永続 + この runtime)。
			if launches >= autoRenameMaxAgentLaunches {
				if !loggedLaunchCap {
					log.Printf("autoname agent launch cap reached user=%s chat=%s launches=%d", username, chatID, launches)
					loggedLaunchCap = true
				}
				continue
			}

			turns, err := cursor.PromptTurnCount(layout.DataDir, project.Path, chatID)
			if err != nil {
				log.Printf("autoname turns user=%s chat=%s: %v", username, chatID, err)
				continue
			}
			handledTurn := state.LastTurn
			if state.ReservedTurn > handledTurn {
				handledTurn = state.ReservedTurn
			}
			if !RenameThreshold(turns) || turns <= handledTurn || busy {
				continue
			}

			prompts, err := cursor.ReadPromptHistory(layout.DataDir, project.Path, chatID)
			if err != nil {
				continue
			}
			// 起動前に回数とターン予約を永続化し、失敗・再接続でも上限を越えさせない。
			state.ReservedTurn = turns
			state.AgentLaunches = launches + 1
			if err := cursor.WriteAutonameState(layout.DataDir, project.Path, chatID, state); err != nil {
				log.Printf("autoname reserve save user=%s chat=%s: %v", username, chatID, err)
				continue
			}
			mu.Lock()
			agentLaunches = state.AgentLaunches
			generating = true
			mu.Unlock()
			log.Printf("autoname agent launch user=%s chat=%s turn=%d launch=%d/%d", username, chatID, turns, state.AgentLaunches, autoRenameMaxAgentLaunches)
			go func(turn int, prompts []string, current string) {
				ctx, cancel := context.WithTimeout(context.Background(), autoRenameNameTimeout)
				defer cancel()
				title, err := s.generateSessionName(ctx, nameGenRequest{
					Username:     username,
					Project:      project,
					Prompts:      prompts,
					CurrentTitle: current,
				})
				mu.Lock()
				generating = false
				if err != nil {
					mu.Unlock()
					log.Printf("autoname generate user=%s chat=%s turn=%d: %v", username, chatID, turn, err)
					return
				}
				title = SanitizeSessionTitle(title)
				if title == "" {
					mu.Unlock()
					log.Printf("autoname empty title user=%s chat=%s turn=%d", username, chatID, turn)
					return
				}
				pendingTitle = title
				pendingTurn = turn
				injectTries = 0
				mu.Unlock()
				log.Printf("autoname pending user=%s chat=%s turn=%d title=%q", username, chatID, turn, title)
			}(turns, prompts, metaTitle)
		}
	}
}

// injectRename は slash コマンド本文と Enter を分けて送る。
// 同一 Write に含めた \r は CLI では本文の改行になり、送信確定にならない。
func injectRename(rt *runtimeSession, title string) error {
	// 入力行に溜まった未送信テキストを消す。
	if _, err := rt.Write([]byte{0x15}); err != nil {
		return err
	}
	time.Sleep(autoRenameClearDelay)
	if _, err := rt.Write([]byte("/rename " + title)); err != nil {
		return err
	}
	time.Sleep(autoRenameEnterDelay)
	_, err := rt.Write([]byte{'\r'})
	return err
}

func (s *Server) generateSessionName(ctx context.Context, req nameGenRequest) (string, error) {
	if s.nameGenerator != nil {
		return s.nameGenerator(ctx, req)
	}
	return s.generateSessionNameWithAgent(ctx, req)
}

func (s *Server) generateSessionNameWithAgent(ctx context.Context, req nameGenRequest) (string, error) {
	layout, err := s.userLayout(req.Username)
	if err != nil {
		return "", err
	}
	command := s.cfg.AgentCommand
	if _, err := exec.LookPath(command); err != nil && !hasSlash(command) {
		return "", fmt.Errorf("agent command: %w", err)
	}
	prompt := buildAutonamePrompt(req.Prompts, req.CurrentTitle)
	known, err := cursor.ChatIDSet(layout.DataDir, req.Project.Path)
	if err != nil {
		known = map[string]struct{}{}
	}
	args := []string{
		"-p",
		"--mode", "ask",
		"--output-format", "text",
		"--trust",
		"--workspace", req.Project.Path,
		prompt,
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = req.Project.Path
	cmd.Env = layout.Environ(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("agent -p: %s", detail)
	}
	// print モードが作った履歴チャットが一覧を汚さないよう隠す。
	if chat, found, err := cursor.FindNewChat(layout.DataDir, req.Project.Path, known); err == nil && found {
		if hideErr := cursor.HideChat(layout.DataDir, req.Project.Path, chat.ID); hideErr != nil {
			log.Printf("autoname hide naming chat %s: %v", chat.ID, hideErr)
		}
	}
	return stdout.String(), nil
}

func buildAutonamePrompt(prompts []string, currentTitle string) string {
	// prompt_history は新しいものが先頭。命名には新しい方を優先しつつ件数を抑える。
	selected := prompts
	if len(selected) > autoRenameMaxPrompts {
		selected = selected[:autoRenameMaxPrompts]
	}
	var b strings.Builder
	b.WriteString("あなたはチャットセッションの短いタイトルを付ける係です。\n")
	b.WriteString("以下は新しい順のユーザー発話です。会話の現在の主題が分かる日本語のタイトルを1つだけ出力してください。\n")
	b.WriteString("制約: タイトル本文のみ。引用符・接頭辞・説明文は禁止。40文字以内。体言止めか短い名詞句。\n")
	if currentTitle != "" {
		b.WriteString("現在のタイトル: ")
		b.WriteString(currentTitle)
		b.WriteByte('\n')
	}
	b.WriteString("\nユーザー発話:\n")
	for i, p := range selected {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(p) > 200 {
			p = string([]rune(p)[:200]) + "…"
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, p)
	}
	return b.String()
}
