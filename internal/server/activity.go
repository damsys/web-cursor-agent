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
	// AgentActivityBusy は処理中、またはユーザー操作待ちである。
	AgentActivityBusy
)

// ClassifyTitle は status indicators の端末タイトルから状態を分類する。
func ClassifyTitle(title string) AgentActivity {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "working"):
		return AgentActivityBusy
	case strings.Contains(lower, "waiting for"):
		return AgentActivityBusy
	case strings.Contains(lower, "ready"):
		return AgentActivityIdle
	default:
		return AgentActivityUnknown
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
