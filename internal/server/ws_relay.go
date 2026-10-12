package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"web-cursor-agent/internal/cursor"
)

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.URL.Query().Get("project"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	chatID := r.URL.Query().Get("chat")
	attachID := r.URL.Query().Get("attach")
	if chatID != "" {
		if !cursor.ValidChatID(chatID) {
			writeJSON(w, http.StatusBadRequest, errorBody("セッションが見つかりません"))
			return
		}
		layout, err := s.userLayout(user.Username)
		if err != nil {
			log.Printf("cursor layout: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("セッションを開始できません"))
			return
		}
		found, err := cursor.HasChat(layout.DataDir, project.Path, chatID)
		if err != nil {
			log.Printf("lookup chat: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("セッションを開始できません"))
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
			return
		}
	}

	var rt *runtimeSession
	switch {
	case attachID != "":
		existing, found := s.runtimes.Lookup(attachID, user.Username)
		if !found || existing.projectID != project.ID {
			writeJSON(w, http.StatusNotFound, errorBody("再接続できるセッションがありません"))
			return
		}
		rt = existing
	case chatID != "":
		if existing, found := s.runtimes.FindByChat(user.Username, project.ID, chatID); found {
			rt = existing
		}
	}
	// 新規起動前に既存 ID を覚え、起動後に現れたチャットを URL 用に紐づける。
	var knownChats map[string]struct{}
	if rt == nil && chatID == "" {
		if layout, err := s.userLayout(user.Username); err == nil {
			knownChats, _ = cursor.ChatIDSet(layout.DataDir, project.Path)
		}
	}
	started := false
	if rt == nil {
		if !s.runtimes.Reserve(user.Username) {
			writeJSON(w, http.StatusTooManyRequests, errorBody("同時に開けるセッション数の上限に達しています"))
			return
		}
		session, err := s.launch(user.Username, project, chatID, 80, 24)
		if err != nil {
			s.runtimes.ReleaseReservation(user.Username)
			log.Printf("start agent user=%s project=%s: %v", user.Username, project.ID, err)
			writeJSON(w, http.StatusInternalServerError, errorBody("エージェントを起動できません"))
			return
		}
		id, err := newRuntimeID()
		if err != nil {
			session.Close()
			s.runtimes.ReleaseReservation(user.Username)
			log.Printf("runtime id: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody("エージェントを起動できません"))
			return
		}
		rt = &runtimeSession{
			id:             id,
			username:       user.Username,
			projectID:      project.ID,
			chatID:         chatID,
			term:           session,
			grace:          s.cfg.DetachGrace,
			hub:            s.runtimes,
			scrollback:     newByteRing(scrollbackMax),
			detachedBuffer: newByteRing(detachBufferMax),
			readerDone:     make(chan struct{}),
		}
		s.runtimes.Track(rt)
		started = true
		log.Printf("agent started user=%s project=%s chat=%s runtime=%s", user.Username, project.ID, chatID, rt.id)
		if chatID == "" {
			s.watchNewChat(rt, user.Username, project, knownChats)
		} else {
			s.startAutoRename(rt, user.Username, project)
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		if started {
			rt.Terminate()
		}
		log.Printf("accept websocket: %v", err)
		return
	}
	conn.SetReadLimit(maxInputBytes + 1024)
	// ページ再読込など空の端末向け。同一画面の再接続では付けず、差分だけを受け取る。
	replay := r.URL.Query().Get("replay") == "1"
	intentionalClose, agentExited := s.relay(r.Context(), conn, rt, replay)
	if agentExited {
		log.Printf("agent stopped user=%s project=%s chat=%s runtime=%s code=%d", user.Username, project.ID, chatID, rt.id, rt.ExitCode())
		return
	}
	if intentionalClose && rt.ShouldTerminateOnClose() {
		log.Printf("agent closed on idle leave user=%s project=%s runtime=%s", user.Username, project.ID, rt.id)
		rt.Terminate()
		return
	}
	rt.DetachOutput()
	log.Printf("agent detached user=%s project=%s runtime=%s intentional=%v activity=%v", user.Username, project.ID, rt.id, intentionalClose, rt.Activity())
}

func (s *Server) relay(ctx context.Context, conn *websocket.Conn, rt *runtimeSession, replay bool) (intentionalClose bool, agentExited bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")

	buffered, live, statusLive, ok := rt.AttachOutput(replay)
	if !ok {
		payload, _ := json.Marshal(map[string]any{"type": "exit", "code": rt.ExitCode()})
		writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
		_ = conn.Write(writeCtx, websocket.MessageText, payload)
		writeCancel()
		return false, true
	}

	var (
		writeMu sync.Mutex
		exited  bool
		exitMu  sync.Mutex
	)
	setExited := func() {
		exitMu.Lock()
		exited = true
		exitMu.Unlock()
	}
	write := func(typ websocket.MessageType, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, typ, data)
	}
	helloBody := map[string]any{
		"type":     "hello",
		"attach":   rt.id,
		"chat":     rt.ChatID(),
		"activity": ActivityName(rt.Activity()),
	}
	if title := rt.SessionTitle(); title != "" {
		helloBody["title"] = title
	}
	hello, _ := json.Marshal(helloBody)
	if err := write(websocket.MessageText, hello); err != nil {
		return false, false
	}
	if len(buffered) > 0 {
		if err := write(websocket.MessageBinary, buffered); err != nil {
			return false, false
		}
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case state, open := <-statusLive:
				if !open {
					statusLive = nil
					continue
				}
				payloadBody := map[string]any{
					"type":  "activity",
					"state": ActivityName(state.Activity),
				}
				if state.Title != "" {
					payloadBody["title"] = state.Title
				}
				if chat := state.ChatID; chat != "" {
					payloadBody["chat"] = chat
				} else if chat := rt.ChatID(); chat != "" {
					payloadBody["chat"] = chat
				}
				payload, _ := json.Marshal(payloadBody)
				if writeErr := write(websocket.MessageText, payload); writeErr != nil {
					cancel()
					return
				}
			case payload, open := <-live:
				if !open {
					// detach/reattach でも sink は閉じる。プロセス終了時だけ exit を送る。
					select {
					case <-rt.Done():
					case <-time.After(200 * time.Millisecond):
					}
					if code := rt.ExitCode(); code >= 0 {
						setExited()
						exitPayload, _ := json.Marshal(map[string]any{"type": "exit", "code": code})
						_ = write(websocket.MessageText, exitPayload)
					}
					cancel()
					return
				}
				if writeErr := write(websocket.MessageBinary, payload); writeErr != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			exitMu.Lock()
			agentExited = exited
			exitMu.Unlock()
			return intentionalClose, agentExited
		}
		var message struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		switch message.Type {
		case "input":
			if len(message.Data) > maxInputBytes {
				continue
			}
			if _, err := rt.Write([]byte(message.Data)); err != nil {
				log.Printf("write pty: %v", err)
				exitMu.Lock()
				agentExited = exited
				exitMu.Unlock()
				return intentionalClose, agentExited
			}
		case "resize":
			if err := rt.Resize(message.Cols, message.Rows); err != nil {
				log.Printf("resize pty: %v", err)
			}
		case "close":
			return true, false
		}
	}
}
