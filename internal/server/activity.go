package server

import (
	"bytes"
	"strings"
)

// AgentActivity は Cursor CLI の端末タイトルから推定した対話状態である。
type AgentActivity int

const (
	// AgentActivityUnknown はタイトル未受信、または判定できないときである。
	AgentActivityUnknown AgentActivity = iota
	// AgentActivityIdle は次の入力待ち (Ready) である。
	AgentActivityIdle
	// AgentActivityBusy は処理中である。
	AgentActivityBusy
	// AgentActivityWaiting は選択・確認などユーザー操作待ちである。
	AgentActivityWaiting
)

// ClassifyTitle は status indicators の端末タイトルから状態を分類する。
func ClassifyTitle(title string) AgentActivity {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "working"):
		return AgentActivityBusy
	case strings.Contains(lower, "waiting for"):
		return AgentActivityWaiting
	case strings.Contains(lower, "ready"):
		return AgentActivityIdle
	default:
		return AgentActivityUnknown
	}
}

// SessionNameFromTitle は status indicators の端末タイトルからセッション概要を取り出す。
// 現行 CLI は "概要 - ✅ Ready" / "概要 - ⏳ Working…" 形式。
// 未命名時の "Cursor Agent - …" は概要なしとして扱う。
func SessionNameFromTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	// 旧形式 "Ready | 概要" も受け付ける。
	if head, tail, ok := strings.Cut(title, " | "); ok {
		if ClassifyTitle(head) != AgentActivityUnknown || ClassifyTitle(title) != AgentActivityUnknown {
			return cleanSessionName(tail)
		}
	}
	// 現行形式: 末尾の " - <status>" を剥がす。概要自体に " - " が含まれる場合も考慮する。
	rest := title
	for {
		idx := strings.LastIndex(rest, " - ")
		if idx < 0 {
			return ""
		}
		name := strings.TrimSpace(rest[:idx])
		status := strings.TrimSpace(rest[idx+3:])
		if name != "" && isStatusSuffix(status) {
			return cleanSessionName(name)
		}
		rest = rest[:idx]
	}
}

func isStatusSuffix(status string) bool {
	if ClassifyTitle(status) != AgentActivityUnknown {
		return true
	}
	lower := strings.ToLower(status)
	return strings.Contains(lower, "loading")
}

func cleanSessionName(name string) string {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "", "cursor agent", "cursor", "agent":
		return ""
	default:
		return name
	}
}

// ActivityName は WebSocket 通知用の状態名を返す。
func ActivityName(activity AgentActivity) string {
	switch activity {
	case AgentActivityIdle:
		return "idle"
	case AgentActivityBusy:
		return "busy"
	case AgentActivityWaiting:
		return "waiting"
	default:
		return "unknown"
	}
}

// oscScanner は PTY 出力から OSC 0/2 のタイトル更新を拾う。
type oscScanner struct {
	pending []byte
	onTitle func(string)
}

// Feed はバイト列を取り込み、完全な OSC タイトルがあれば onTitle を呼ぶ。
func (s *oscScanner) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	s.pending = append(s.pending, data...)
	for {
		start := bytes.Index(s.pending, []byte{0x1b, ']'})
		if start < 0 {
			if len(s.pending) > 1 {
				s.pending = s.pending[len(s.pending)-1:]
			}
			return
		}
		if start > 0 {
			s.pending = s.pending[start:]
		}
		rest := s.pending[2:]
		bel := bytes.IndexByte(rest, 0x07)
		st := bytes.Index(rest, []byte{0x1b, '\\'})
		end := -1
		endLen := 0
		switch {
		case bel >= 0 && (st < 0 || bel < st):
			end = bel
			endLen = 1
		case st >= 0:
			end = st
			endLen = 2
		default:
			if len(s.pending) > 4096 {
				s.pending = s.pending[len(s.pending)-2:]
			}
			return
		}
		payload := rest[:end]
		s.pending = rest[end+endLen:]
		ps, pt, ok := bytes.Cut(payload, []byte{';'})
		if !ok {
			continue
		}
		if string(ps) != "0" && string(ps) != "2" {
			continue
		}
		if s.onTitle != nil {
			s.onTitle(string(pt))
		}
	}
}
