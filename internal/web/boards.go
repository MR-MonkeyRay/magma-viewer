package web

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"magma-viewer/internal/board"
	"magma-viewer/internal/fetcher"
)

// resolveBoard keeps unqualified file links bound to the migrated board.
func (s *Server) resolveBoard(w http.ResponseWriter, r *http.Request, file bool) (*board.Runtime, board.Board, bool) {
	id := r.URL.Query().Get("board")
	if id == "" {
		id = s.boards.LegacyID()
	}
	rt, b, err := s.boards.Resolve(id)
	if err != nil && r.URL.Query().Get("board") == "" && !file {
		rt, b, err = s.boards.Resolve(s.boards.Suggested(false))
	}
	if err != nil {
		if file {
			http.NotFound(w, r)
		} else {
			writeBoardError(w, err)
		}
		return nil, board.Board{}, false
	}
	return rt, b, true
}

// defaultBoard chooses status information for the legacy admin page and readiness.
func (s *Server) defaultBoard() (*board.Runtime, board.Board, error) {
	if id := s.boards.LegacyID(); id != "" {
		if rt, b, err := s.boards.Resolve(id); err == nil {
			return rt, b, nil
		}
	}
	for _, b := range s.boards.All() {
		if rt, b, err := s.boards.Resolve(b.ID); err == nil {
			return rt, b, nil
		}
	}
	return nil, board.Board{}, board.ErrNotFound
}

func tokenInfo(tok fetcher.TokenInfo) any {
	if tok.Cookie == "" {
		return nil
	}
	return tok
}

func (s *Server) handleBoards(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID     string    `json:"id"`
		Name   string    `json:"name"`
		Count  int       `json:"count"`
		Latest time.Time `json:"latest,omitzero"`
		State  string    `json:"state"`
		Hidden *bool     `json:"hidden,omitempty"`
	}
	admin := s.auth.isAdmin(r)
	items := make([]item, 0)
	for _, b := range s.boards.List(admin) {
		it := item{ID: b.ID, Name: b.Name, Count: b.Count, Latest: b.Latest, State: b.State}
		if admin {
			it.Hidden = &b.Hidden
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "suggested": s.boards.Suggested(false), "legacy_id": s.boards.LegacyID(),
	})
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, _ *http.Request) {
	boards := s.boards.List(true)
	d := diskSpace(s.cfg.Storage.Dir)
	if d != nil {
		for _, b := range boards {
			d.Archive += b.Bytes
		}
	}
	_, cookie := s.creds.Cookie()
	writeJSON(w, http.StatusOK, map[string]any{
		"boards": boards, "shared_cookie": cookie, "shared_token": tokenInfo(s.creds.Token()),
		"notify": s.Notifier != nil, "schedule": s.cfg.Schedule, "disk": d, "version": s.Version,
	})
}

func (s *Server) handleAdminBoards(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		board.Summary
		Cookie fetcher.CookieInfo `json:"cookie"`
		Token  any                `json:"token"`
	}
	items := make([]item, 0)
	for _, b := range s.boards.List(true) {
		rt, _, err := s.boards.Resolve(b.ID)
		if err == nil {
			items = append(items, item{Summary: b, Cookie: rt.Fetcher.Cookie(), Token: tokenInfo(rt.Fetcher.Token())})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSaveBoard(w http.ResponseWriter, r *http.Request) {
	var settings board.Settings
	if !readJSON(w, r, 16<<10, &settings) {
		return
	}
	var summary board.Summary
	var err error
	status := http.StatusOK
	if r.Method == http.MethodPost {
		summary, err = s.boards.Create(settings)
		status = http.StatusCreated
	} else {
		summary, err = s.boards.Update(r.PathValue("id"), settings)
	}
	if err != nil {
		writeBoardError(w, err)
		return
	}
	writeJSON(w, status, summary)
}

func writeBoardError(w http.ResponseWriter, err error) {
	var invalid board.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Code, invalid.Msg)
	case errors.Is(err, board.ErrNotFound):
		writeError(w, http.StatusNotFound, "board_not_found", "画板不存在")
	case errors.Is(err, fetcher.ErrBusy):
		writeError(w, http.StatusConflict, "board_busy", "正在抓取，请稍后再试")
	default:
		log.Printf("board: %v", err)
		writeError(w, http.StatusInternalServerError, "board_save_failed", "保存画板失败")
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	type item struct {
		fetcher.Run
		BoardID   string `json:"board_id"`
		BoardName string `json:"board_name"`
	}
	boards := s.boards.List(true)
	if id := r.URL.Query().Get("board"); id != "" {
		summary, err := s.boards.Summary(id)
		if err != nil {
			writeBoardError(w, err)
			return
		}
		boards = []board.Summary{summary}
	}
	items := make([]item, 0)
	for _, b := range boards {
		rt, _, err := s.boards.Resolve(b.ID)
		if err != nil {
			continue
		}
		for _, run := range rt.Fetcher.History() {
			items = append(items, item{Run: run, BoardID: b.ID, BoardName: b.Name})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int { return b.Time.Compare(a.Time) })
	items = items[:min(len(items), 200)]
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) cookieInfo(rt *board.Runtime) map[string]any {
	_, info := s.creds.Cookie()
	token := s.creds.Token()
	if rt != nil {
		info, token = rt.Fetcher.Cookie(), rt.Fetcher.Token()
	}
	body := map[string]any{"source": info.Source, "updated": info.Updated, "token": tokenInfo(token)}
	if rt != nil {
		body["override"] = info.Source == "board"
	}
	return body
}

func (s *Server) handleCookieInfo(w http.ResponseWriter, r *http.Request) {
	var rt *board.Runtime
	if r.URL.Query().Get("board") != "" {
		var ok bool
		rt, _, ok = s.resolveBoard(w, r, false)
		if !ok {
			return
		}
	}
	writeJSON(w, http.StatusOK, s.cookieInfo(rt))
}

func (s *Server) handleCookie(w http.ResponseWriter, r *http.Request) {
	var rt *board.Runtime
	if r.URL.Query().Get("board") != "" {
		var ok bool
		rt, _, ok = s.resolveBoard(w, r, false)
		if !ok {
			return
		}
	}
	var body struct {
		Cookie string `json:"cookie"`
	}
	if !readJSON(w, r, 16<<10, &body) {
		return
	}
	var err error
	if rt == nil {
		err = s.boards.SetSharedCookie(body.Cookie)
	} else {
		err = rt.Fetcher.SetCookie(body.Cookie)
	}
	if err != nil {
		writeCookieError(w, err)
		return
	}
	if rt == nil {
		rt, _, err = s.defaultBoard()
		if err != nil {
			writeJSON(w, http.StatusOK, s.cookieInfo(nil))
			return
		}
	}
	s.writeStatus(w, rt)
}

func (s *Server) handleDeleteCookie(w http.ResponseWriter, r *http.Request) {
	var rt *board.Runtime
	if r.URL.Query().Get("board") != "" {
		var ok bool
		rt, _, ok = s.resolveBoard(w, r, false)
		if !ok {
			return
		}
	}
	var err error
	if rt == nil {
		err = s.boards.ClearSharedCookie()
	} else {
		err = rt.Fetcher.ClearCookie()
	}
	if err != nil {
		writeCookieError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cookieInfo(rt))
}

func writeCookieError(w http.ResponseWriter, err error) {
	if errors.Is(err, fetcher.ErrInvalidCookie) {
		writeError(w, http.StatusBadRequest, "invalid_cookie", "Cookie 格式不正确")
		return
	}
	log.Printf("cookie: %v", err)
	writeError(w, http.StatusInternalServerError, "cookie_save_failed", "保存 Cookie 失败")
}
