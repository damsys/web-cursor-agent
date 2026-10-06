package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"sync"
	"time"

	"web-cursor-agent/internal/terminal"
)

const (
	// scrollbackMax は接続中も含め端末出力を保持する上限。ページ再読込時の replay に使う。
	scrollbackMax   = 512 * 1024
	detachBufferMax = scrollbackMax
	outputSinkSize  = 64
)

// runtimeSession は WebSocket の寿命から切り離した 1 つの agent 対話である。
type runtimeSession struct {
	id        string
	username  string
	projectID string
	chatID    string
	term      *terminal.Session
	grace     time.Duration
	hub       *runtimeHub

	mu             sync.Mutex
	activity       AgentActivity
	sink           chan []byte
	activitySink   chan AgentActivity
	scrollback     *byteRing
	detachedBuffer *byteRing
	graceTimer     *time.Timer
	closed         bool
	readerDone     chan struct{}
}

type byteRing struct {
	buf []byte
	max int
}

func newByteRing(max int) *byteRing {
	return &byteRing{max: max}
}

func (r *byteRing) Write(p []byte) {
	if r.max <= 0 {
		return
	}
	if len(p) >= r.max {
		r.buf = append([]byte(nil), p[len(p)-r.max:]...)
		return
	}
	overflow := len(r.buf) + len(p) - r.max
	if overflow > 0 {
		r.buf = r.buf[overflow:]
	}
	r.buf = append(r.buf, p...)
}

func (r *byteRing) Take() []byte {
	out := r.buf
	r.buf = nil
	return out
}

// Snapshot は保持内容のコピーを返す。リング自体は消さない。
func (r *byteRing) Snapshot() []byte {
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

func newRuntimeID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (rt *runtimeSession) start() {
	go rt.readLoop()
}

func (rt *runtimeSession) readLoop() {
	defer close(rt.readerDone)
	defer rt.closeSink()
	buf := make([]byte, 32*1024)
	scanner := &oscScanner{onTitle: rt.setTitle}
	for {
		n, err := rt.term.Read(buf)
		if n > 0 {
			payload := append([]byte(nil), buf[:n]...)
			scanner.Feed(payload)
			rt.deliver(payload)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("read pty runtime=%s: %v", rt.id, err)
			}
			rt.markClosed()
			rt.hub.remove(rt)
			return
		}
	}
}

func (rt *runtimeSession) closeSink() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sink != nil {
		close(rt.sink)
		rt.sink = nil
	}
	if rt.activitySink != nil {
		close(rt.activitySink)
		rt.activitySink = nil
	}
}

func (rt *runtimeSession) markClosed() {
	rt.mu.Lock()
	rt.closed = true
	rt.stopGraceLocked()
	rt.mu.Unlock()
}

func (rt *runtimeSession) setTitle(title string) {
	activity := ClassifyTitle(title)
	if activity == AgentActivityUnknown {
		return
	}
	rt.mu.Lock()
	changed := rt.activity != activity
	rt.activity = activity
	sink := rt.activitySink
	rt.mu.Unlock()
	if !changed || sink == nil {
		return
	}
	// 最新の状態だけを届ける。満杯なら古い値を捨てて差し替える。
	select {
	case sink <- activity:
	default:
		select {
		case <-sink:
		default:
		}
		select {
		case sink <- activity:
		default:
		}
	}
}

func (rt *runtimeSession) Activity() AgentActivity {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.activity
}

func (rt *runtimeSession) deliver(payload []byte) {
	rt.mu.Lock()
	// 接続中も scrollback へ tee し、ページ再読込後の空画面を避けられるようにする。
	rt.scrollback.Write(payload)
	sink := rt.sink
	if sink == nil {
		rt.detachedBuffer.Write(payload)
		rt.mu.Unlock()
		return
	}
	rt.mu.Unlock()
	select {
	case sink <- payload:
	case <-rt.readerDone:
	}
}

// AttachOutput は再接続用の出力を返し、以降の出力と状態変化を channel へ流す。
// replay が true のときは保持している scrollback 全体を返す (ページ再読込向け)。
// false のときは切断中に溜まった差分だけを返す (同一画面の再接続向け)。
func (rt *runtimeSession) AttachOutput(replay bool) (buffered []byte, live <-chan []byte, activity <-chan AgentActivity, ok bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil, nil, nil, false
	}
	rt.stopGraceLocked()
	if rt.sink != nil {
		close(rt.sink)
		rt.sink = nil
	}
	if rt.activitySink != nil {
		close(rt.activitySink)
		rt.activitySink = nil
	}
	if replay {
		buffered = rt.scrollback.Snapshot()
		_ = rt.detachedBuffer.Take()
	} else {
		buffered = rt.detachedBuffer.Take()
	}
	sink := make(chan []byte, outputSinkSize)
	rt.sink = sink
	activitySink := make(chan AgentActivity, 1)
	rt.activitySink = activitySink
	return buffered, sink, activitySink, true
}

// DetachOutput は WebSocket 切断後も agent を残し、出力をバッファへ回す。
func (rt *runtimeSession) DetachOutput() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return
	}
	if rt.sink != nil {
		close(rt.sink)
		rt.sink = nil
	}
	if rt.activitySink != nil {
		close(rt.activitySink)
		rt.activitySink = nil
	}
	rt.startGraceLocked()
}

// ShouldTerminateOnClose は意図的な離脱時に、アイドルなら即終了してよいかを返す。
func (rt *runtimeSession) ShouldTerminateOnClose() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.activity == AgentActivityIdle
}

func (rt *runtimeSession) startGraceLocked() {
	if rt.graceTimer != nil {
		rt.graceTimer.Stop()
	}
	if rt.grace <= 0 {
		rt.graceTimer = nil
		go rt.Terminate()
		return
	}
	rt.graceTimer = time.AfterFunc(rt.grace, func() {
		log.Printf("detach grace expired runtime=%s user=%s project=%s", rt.id, rt.username, rt.projectID)
		rt.Terminate()
	})
}

func (rt *runtimeSession) stopGraceLocked() {
	if rt.graceTimer != nil {
		rt.graceTimer.Stop()
		rt.graceTimer = nil
	}
}

// Terminate は agent を終了し、同時セッション枠を返す。
func (rt *runtimeSession) Terminate() {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return
	}
	rt.closed = true
	rt.stopGraceLocked()
	if rt.sink != nil {
		close(rt.sink)
		rt.sink = nil
	}
	if rt.activitySink != nil {
		close(rt.activitySink)
		rt.activitySink = nil
	}
	rt.mu.Unlock()
	rt.term.Close()
	rt.hub.remove(rt)
}

// ExitCode はプロセスの終了コードを返す。
func (rt *runtimeSession) ExitCode() int {
	return rt.term.ExitCode()
}

// Done はプロセス終了を待つ。
func (rt *runtimeSession) Done() <-chan struct{} {
	return rt.term.Done()
}

// Write は仮想端末へ入力する。
func (rt *runtimeSession) Write(p []byte) (int, error) {
	return rt.term.Write(p)
}

// Resize は仮想端末サイズを変える。
func (rt *runtimeSession) Resize(cols, rows int) error {
	return rt.term.Resize(cols, rows)
}

type runtimeHub struct {
	mu       sync.Mutex
	max      int
	counts   map[string]int
	byID     map[string]*runtimeSession
	sessions map[*runtimeSession]struct{}
}

func newRuntimeHub(max int) *runtimeHub {
	return &runtimeHub{
		max:      max,
		counts:   map[string]int{},
		byID:     map[string]*runtimeSession{},
		sessions: map[*runtimeSession]struct{}{},
	}
}

// Reserve は新規起動用の枠を確保する。
func (h *runtimeHub) Reserve(username string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts[username] >= h.max {
		return false
	}
	h.counts[username]++
	return true
}

// ReleaseReservation は起動失敗時に枠だけ返す。
func (h *runtimeHub) ReleaseReservation(username string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts[username] > 0 {
		h.counts[username]--
	}
}

// Track は起動済みの runtime を登録する。Reserve 済みの枠をそのまま使う。
func (h *runtimeHub) Track(rt *runtimeSession) {
	h.mu.Lock()
	h.byID[rt.id] = rt
	h.sessions[rt] = struct{}{}
	h.mu.Unlock()
	rt.start()
}

// Lookup は attach ID から runtime を返す。所有者以外には渡さない。
func (h *runtimeHub) Lookup(id, username string) (*runtimeSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rt, ok := h.byID[id]
	if !ok || rt.username != username {
		return nil, false
	}
	rt.mu.Lock()
	closed := rt.closed
	rt.mu.Unlock()
	if closed {
		return nil, false
	}
	return rt, true
}

// FindByChat は同じユーザー・プロジェクト・チャットの生存 runtime を返す。
func (h *runtimeHub) FindByChat(username, projectID, chatID string) (*runtimeSession, bool) {
	if chatID == "" {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for rt := range h.sessions {
		if rt.username == username && rt.projectID == projectID && rt.chatID == chatID {
			rt.mu.Lock()
			closed := rt.closed
			rt.mu.Unlock()
			if !closed {
				return rt, true
			}
		}
	}
	return nil, false
}

func (h *runtimeHub) remove(rt *runtimeSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[rt]; !ok {
		return
	}
	delete(h.sessions, rt)
	delete(h.byID, rt.id)
	if h.counts[rt.username] > 0 {
		h.counts[rt.username]--
	}
}

// CloseAll はサーバ停止時に残っている agent を終了する。
func (h *runtimeHub) CloseAll() {
	h.mu.Lock()
	sessions := make([]*runtimeSession, 0, len(h.sessions))
	for rt := range h.sessions {
		sessions = append(sessions, rt)
	}
	h.mu.Unlock()
	for _, rt := range sessions {
		rt.Terminate()
	}
}
