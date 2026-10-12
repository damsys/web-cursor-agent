package server

import (
	"errors"
	"log"
	"net/http"
	"time"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
)

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(w, r); !ok {
		return
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	items := make([]item, 0, len(s.cfg.Projects))
	for _, project := range s.cfg.Projects {
		items = append(items, item{ID: project.ID, Name: project.Name, Path: project.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	layout, err := s.userLayout(user.Username)
	if err != nil {
		log.Printf("cursor layout: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッション一覧を読み取れません"))
		return
	}
	chats, err := cursor.ListChats(layout.DataDir, project.Path)
	if err != nil {
		log.Printf("list chats: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッション一覧を読み取れません"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": chats})
}

func (s *Server) handleHideSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	project, ok := s.cfg.Project(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("プロジェクトが見つかりません"))
		return
	}
	chatID := r.PathValue("chatId")
	if !cursor.ValidChatID(chatID) {
		writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
		return
	}
	layout, err := s.userLayout(user.Username)
	if err != nil {
		log.Printf("cursor layout: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッションを非表示にできません"))
		return
	}
	if err := cursor.HideChat(layout.DataDir, project.Path, chatID); err != nil {
		if errors.Is(err, cursor.ErrChatNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("セッションが見つかりません"))
			return
		}
		log.Printf("hide chat: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("セッションを非表示にできません"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) userLayout(username string) (cursor.Layout, error) {
	layout, err := cursor.LayoutFor(s.cfg.StateDir, username)
	if err != nil {
		return cursor.Layout{}, err
	}
	if err := layout.Ensure(); err != nil {
		return cursor.Layout{}, err
	}
	return layout, nil
}

// watchNewChat は agent が作ったチャット履歴 ID を検出し、runtime とクライアントへ渡す。
func (s *Server) watchNewChat(rt *runtimeSession, username string, project config.Project, known map[string]struct{}) {
	if known == nil {
		known = map[string]struct{}{}
	}
	go func() {
		layout, err := s.userLayout(username)
		if err != nil {
			return
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-rt.Done():
				return
			case <-ticker.C:
				chat, found, err := cursor.FindNewChat(layout.DataDir, project.Path, known)
				if err != nil || !found {
					continue
				}
				if s.runtimes.BindChat(rt, chat.ID) {
					log.Printf("chat bound user=%s project=%s chat=%s runtime=%s", username, project.ID, chat.ID, rt.id)
					s.startAutoRename(rt, username, project)
					return
				}
				// 他 runtime が先に取った ID は known に足して次を探す。
				known[chat.ID] = struct{}{}
			}
		}
	}()
}
