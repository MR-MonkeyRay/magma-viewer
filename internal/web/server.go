// Package web serves the gallery UI, image files and download endpoints.
package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"magma-viewer/internal/board"
	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
	"magma-viewer/internal/notice"
	"magma-viewer/internal/notify"
	"magma-viewer/internal/store"
)

// GuestZipLimit is the maximum number of images a guest may zip at once.
const GuestZipLimit = 10

const (
	defaultPageSize = 48
	minPageSize     = 12
	maxPageSize     = 200
)

type Server struct {
	cfg     *config.Config
	boards  *board.Manager
	creds   *fetcher.Credentials
	notices *notice.Store
	auth    *auth
	assets  map[string]asset // embedded page files by path

	zones    sync.Map // tz name -> *time.Location
	overview struct {
		mu      sync.Mutex
		byBoard map[string]*overviewCache
	}

	// Version is the release shown in the page footer.
	Version string
	// Notifier sends test alerts from the admin page; nil when no webhook is configured.
	Notifier *notify.Notifier
}

// Go's built-in table lacks woff2, and slim images ship no /etc/mime.types.
func init() { _ = mime.AddExtensionType(".woff2", "font/woff2") }

func New(cfg *config.Config, boards *board.Manager, creds *fetcher.Credentials, ns *notice.Store, static fs.FS) *Server {
	a := newAuth(cfg.Admin.Username, cfg.Admin.Password, cfg.Admin.SessionSecret, cfg.Admin.SessionTTL)
	a.proxies = cfg.TrustedProxyPrefixes()
	assets, err := loadAssets(static)
	if err != nil {
		log.Printf("web: %v", err) // only an unreadable embedded FS gets here
	}
	return &Server{
		cfg:     cfg,
		boards:  boards,
		creds:   creds,
		notices: ns,
		auth:    a,
		assets:  assets,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleAsset)
	mux.HandleFunc("GET /admin", s.handleAsset)
	mux.HandleFunc("GET /admin/{$}", func(w http.ResponseWriter, r *http.Request) {
		target := "/admin"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /readyz", s.handleReady)

	mux.HandleFunc("GET /api/boards", s.handleBoards)
	mux.HandleFunc("GET /api/admin/overview", s.adminOnly(s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/boards", s.adminOnly(s.handleAdminBoards))
	mux.HandleFunc("POST /api/admin/boards", s.adminOnly(s.handleSaveBoard))
	mux.HandleFunc("PUT /api/admin/boards/{id}", s.adminOnly(s.handleSaveBoard))
	mux.HandleFunc("POST /api/admin/boards/{id}/archive", s.adminOnly(s.handleArchiveBoard))
	mux.HandleFunc("DELETE /api/admin/boards/{id}", s.adminOnly(s.handleDeleteBoard))
	mux.HandleFunc("GET /api/admin/boards/{id}/operation", s.adminOnly(s.handleBoardOperation))
	mux.HandleFunc("GET /api/admin/archives", s.adminOnly(s.handleArchives))
	mux.HandleFunc("GET /api/admin/archives/{id}/download", sameOriginOnly(s.adminOnly(s.handleDownloadArchive)))
	mux.HandleFunc("DELETE /api/admin/archives/{id}", s.adminOnly(s.handleDeleteArchive))
	mux.HandleFunc("GET /api/admin/history", s.adminOnly(s.handleHistory))
	mux.HandleFunc("GET /api/cookie", s.adminOnly(s.handleCookieInfo))
	mux.HandleFunc("DELETE /api/cookie", s.adminOnly(s.handleDeleteCookie))
	mux.HandleFunc("GET /api/images", s.handleList)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/status", s.adminOnly(s.handleStatus))
	mux.HandleFunc("POST /api/fetch", s.adminOnly(s.handleFetch))
	mux.HandleFunc("POST /api/schedule", s.adminOnly(s.handleSchedule))
	mux.HandleFunc("PUT /api/cookie", s.adminOnly(s.handleCookie))
	mux.HandleFunc("POST /api/notify/test", s.adminOnly(s.handleNotifyTest))
	mux.HandleFunc("GET /api/notices", s.handleNotices)
	mux.HandleFunc("GET /api/notices/all", s.adminOnly(s.handleAllNotices))
	mux.HandleFunc("POST /api/notices", s.adminOnly(s.handleSaveNotice))
	mux.HandleFunc("PUT /api/notices/{id}", s.adminOnly(s.handleSaveNotice))
	mux.HandleFunc("DELETE /api/notices/{id}", s.adminOnly(s.handleDeleteNotice))

	mux.HandleFunc("GET /img/{name}", s.handleImage)
	mux.HandleFunc("GET /thumb/{name}", s.handleThumb(store.Small))
	mux.HandleFunc("GET /preview/{name}", s.handleThumb(store.Preview))
	mux.HandleFunc("GET /download/zip", s.handleZipSelected)
	mux.HandleFunc("POST /download/zip", s.handleZipSelected) // form post: a large selection outgrows a URL
	mux.HandleFunc("GET /download/all", sameOriginOnly(s.adminOnly(s.handleZipAll)))
	mux.HandleFunc("GET /download/{name}", s.handleDownload)
	// Rejects cross-origin POST/PUT/DELETE from browsers; SameSite=Lax alone
	// still lets a sibling subdomain post with the session cookie.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(denyCrossOrigin))
	return logRequests(securityHeaders(gzipAPI(cop.Handler(mux))))
}

func denyCrossOrigin(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusForbidden, "cross_origin", "拒绝跨站请求")
}

// securityHeaders mirrors the nginx sample, so a deployment without the proxy gets them too.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hd.Set("Content-Security-Policy", contentSecurityPolicy)
		h.ServeHTTP(w, r)
	})
}

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// sameOriginOnly refuses a GET that another site started (a link or redirect
// carries the Lax session cookie), for endpoints too costly to trigger by accident.
func sameOriginOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !startedHere(r) {
			denyCrossOrigin(w, r)
			return
		}
		h(w, r)
	}
}

// startedHere reports whether a request came from this site's own pages or
// the address bar. Browsers send no Fetch Metadata to plain-http origins, so
// Origin and then Referer stand in; a request proving neither is refused.
func startedHere(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		for _, h := range []string{"Origin", "Referer"} {
			if v := r.Header.Get(h); v != "" {
				u, err := url.Parse(v)
				return err == nil && u.Host == r.Host
			}
		}
	}
	return false
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if d := time.Since(start); d > 2*time.Second {
			log.Printf("http: slow %s %s %s", r.Method, r.URL.Path, d)
		}
	})
}

// API responses depend on the session cookie, so no cache (browser or CDN) may keep them.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeCachedJSON sends an already encoded 200 response.
func writeCachedJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// writeError answers with a Chinese message and a stable code the page translates.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.isAdmin(r) {
			writeError(w, http.StatusUnauthorized, "auth_required", "需要管理员登录")
			return
		}
		h(w, r)
	}
}

func intParam(r *http.Request, key string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return v
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, false)
	if !ok {
		return
	}
	size := min(max(intParam(r, "size", defaultPageSize), minPageSize), maxPageSize)
	asc := r.URL.Query().Get("sort") == "asc"
	// around= or date= ask for the page holding that picture or day instead.
	items, total, page, focus := rt.Store.ListAt(intParam(r, "page", 1), size, asc, s.locator(r, asc))
	task := rt.Fetcher.Status()
	pages := max((total+size-1)/size, 1)
	writeJSON(w, http.StatusOK, map[string]any{
		"board":       rt.ID,
		"items":       items,
		"total":       total,
		"page":        page,
		"size":        size,
		"pages":       pages,
		"admin":       s.auth.isAdmin(r),
		"guest_limit": GuestZipLimit,
		"version":     s.Version,
		// Public subset of the fetch status; errors stay admin-only.
		"task": map[string]any{
			"paused": task.Paused, "last_fetched": task.LastFetched, "next_run": s.boards.NextRun(rt.ID),
			"last_run": task.LastRun, "result": task.Result, "code": task.Code,
			// Scheduled runs before retry_at are skipped while failures keep coming.
			"failures": task.Failures, "retry_at": task.RetryAt,
		},
		// Lets polling notice a new announcement without fetching the list.
		"notice": s.notices.Latest(),
		"focus":  focus, // the picture around= or date= located, "" otherwise
	})
}

// locator turns an around=<name> or date=YYYY-MM-DD (&tz=) query into a
// store.Locator; nil when the request asks for neither.
func (s *Server) locator(r *http.Request, asc bool) store.Locator {
	q := r.URL.Query()
	if name := q.Get("around"); name != "" {
		return store.ByName(name)
	}
	day, err := time.ParseInLocation(time.DateOnly, q.Get("date"), s.zone(q.Get("tz")))
	if err != nil {
		return nil
	}
	return store.ByDay(day, asc)
}

// zone resolves a viewer's IANA zone name, falling back to the server's.
func (s *Server) zone(tz string) *time.Location {
	// Only canonical names: aliases such as Europe/./Paris would each take a cache slot.
	if tz != "" && len(tz) <= 64 && path.Clean(tz) == tz {
		if l := s.location(tz); l != nil {
			return l
		}
	}
	return time.Local
}

// handleOverview feeds the timeline strip and archive facts beside the latest picture.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	// Days and hours are counted in the viewer's zone (an IANA name such as Asia/Shanghai) so they match the
	// times the page shows; an unknown or missing zone falls back to the server's.
	rt, _, ok := s.resolveBoard(w, r, false)
	if !ok {
		return
	}
	writeCachedJSON(w, s.overviewJSON(rt, s.zone(r.URL.Query().Get("tz"))))
}

type overviewCache struct {
	gen    uint64
	byZone map[string][]byte
}

// overviewJSON caches each board and zone until the index changes.
func (s *Server) overviewJSON(rt *board.Runtime, loc *time.Location) []byte {
	gen := rt.Store.Generation()
	c := &s.overview
	c.mu.Lock()
	if c.byBoard == nil {
		c.byBoard = map[string]*overviewCache{}
	}
	entry := c.byBoard[rt.ID]
	if entry == nil || entry.gen != gen {
		entry = &overviewCache{gen: gen, byZone: map[string][]byte{}}
		c.byBoard[rt.ID] = entry
	}
	body, ok := entry.byZone[loc.String()]
	c.mu.Unlock()
	if ok {
		return body
	}
	// 96 quarter hours feed the reel's heat strip; playback is capped at the newest 400 pictures.
	body, _ = json.Marshal(rt.Store.Overview(24*time.Hour, 96, 400, loc))
	c.mu.Lock()
	if c.byBoard[rt.ID] == entry {
		entry.byZone[loc.String()] = body
	}
	c.mu.Unlock()
	return body
}

// location loads an IANA zone once; nil when the name is unknown.
func (s *Server) location(tz string) *time.Location {
	if l, ok := s.zones.Load(tz); ok {
		return l.(*time.Location)
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		return nil // not cached: the map must not grow with junk names
	}
	s.zones.Store(tz, l)
	return l
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.auth.clientIP(r)
	if !s.auth.beginAttempt(ip) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "尝试次数过多，请稍后再试")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
		return
	}
	if !s.auth.checkCredentials(body.Username, body.Password) {
		writeError(w, http.StatusUnauthorized, "bad_credentials", "用户名或密码错误")
		return
	}
	s.auth.clearFailures(ip)
	s.auth.setSession(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"admin": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	clearSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"admin": false})
}

// disk is the space around the archive.
type disk struct {
	Free    int64 `json:"free"`
	Total   int64 `json:"total"`
	Archive int64 `json:"archive"` // bytes taken by the indexed pictures
}

func (s *Server) statusBody(rt *board.Runtime) (map[string]any, error) {
	summary, err := s.boards.Summary(rt.ID)
	if err != nil {
		return nil, err
	}
	d := diskSpace(rt.Store.Dir())
	if d != nil {
		d.Archive = summary.Bytes
	}
	return map[string]any{
		"fetch":    rt.Fetcher.Status(),
		"token":    tokenInfo(rt.Fetcher.Token()),
		"cookie":   rt.Fetcher.Cookie(),
		"history":  rt.Fetcher.History(),
		"notify":   s.Notifier != nil,
		"disk":     d,
		"schedule": summary.Schedule,
		"next_run": summary.NextRun,
		"count":    summary.Count,
		"board":    summary,
	}, nil
}

func (s *Server) writeStatus(w http.ResponseWriter, rt *board.Runtime) {
	body, err := s.statusBody(rt)
	if err != nil {
		writeBoardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// handleReady checks storage for every active board. Fetch failures remain informational.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	var st fetcher.Status
	if rt, _, err := s.defaultBoard(); err == nil {
		st = rt.Fetcher.Status()
	}
	writable, count := true, 0
	for _, b := range s.boards.All() {
		if b.Lifecycle != board.Active {
			continue
		}
		count++
		rt, _, err := s.boards.Resolve(b.ID)
		if err == nil {
			err = rt.Store.Writable()
		}
		if err != nil {
			log.Printf("readyz: board %s storage not writable: %v", b.ID, err)
			writable = false
		}
	}
	code := http.StatusOK
	if !writable {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"ok": writable, "storage_writable": writable,
		"last_fetched": st.LastFetched, "failures": st.Failures, "boards": count,
	})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if s.Notifier == nil {
		writeError(w, http.StatusConflict, "notify_disabled", "未配置通知 webhook")
		return
	}
	if err := s.Notifier.Post(r.Context(), notify.EventTest, "这是一条测试通知，收到即表示配置正确。"); err != nil {
		log.Printf("notify test: %v", err)
		writeError(w, http.StatusBadGateway, "notify_failed", "发送失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, false)
	if !ok {
		return
	}
	s.writeStatus(w, rt)
}

func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Magma.Timeout+10*time.Second)
	defer cancel()
	if _, err := rt.Fetcher.Run(ctx); err != nil {
		switch {
		case errors.Is(err, fetcher.ErrBusy):
			writeError(w, http.StatusConflict, "board_busy", "正在抓取，请稍后再试")
		case errors.Is(err, fetcher.ErrNoURL):
			writeError(w, http.StatusConflict, "no_url", "未设置画板地址")
		case errors.Is(err, fetcher.ErrInactive):
			writeError(w, http.StatusConflict, "board_inactive", "画板当前不可抓取")
		default:
			log.Printf("fetch: %v", err)
			writeError(w, http.StatusInternalServerError, "fetch_failed", "抓取失败")
		}
		return
	}
	s.writeStatus(w, rt)
}

// readJSON decodes a JSON request body of at most limit bytes into v, answering the error itself.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	// A cross-origin form cannot send JSON without a CORS preflight.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "请求格式错误")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
		return false
	}
	return true
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Paused *bool `json:"paused"`
	}
	if !readJSON(w, r, 4096, &body) {
		return
	}
	if body.Paused == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
		return
	}
	if err := s.boards.SetPaused(rt.ID, *body.Paused); err != nil {
		if errors.Is(err, board.ErrNotFound) {
			writeBoardError(w, err)
			return
		}
		log.Printf("schedule: %v", err)
		writeError(w, http.StatusInternalServerError, "schedule_save_failed", "保存定时任务状态失败")
		return
	}
	s.writeStatus(w, rt)
}

func (s *Server) handleNotices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.notices.Published()})
}

func (s *Server) handleAllNotices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.notices.All()})
}

// handleSaveNotice creates (POST) or updates (PUT) a notice and answers with it and the full list.
func (s *Server) handleSaveNotice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Publish bool   `json:"publish"`
	}
	// The body limit is in characters; JSON escapes can take six bytes for one.
	if !readJSON(w, r, 6*(notice.MaxTitle+notice.MaxBody)+4096, &body) {
		return
	}
	n, err := s.notices.Save(r.PathValue("id"), body.Title, body.Body, body.Publish)
	if err != nil {
		writeNoticeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notice": n, "items": s.notices.All()})
}

func (s *Server) handleDeleteNotice(w http.ResponseWriter, r *http.Request) {
	if err := s.notices.Delete(r.PathValue("id")); err != nil {
		writeNoticeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.notices.All()})
}

func writeNoticeError(w http.ResponseWriter, err error) {
	var invalid notice.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Code, invalid.Msg)
	case errors.Is(err, notice.ErrNotFound):
		writeError(w, http.StatusNotFound, "notice_not_found", err.Error())
	default:
		log.Printf("notice: %v", err)
		writeError(w, http.StatusInternalServerError, "notice_save_failed", "保存公告失败")
	}
}

// serveFile sends path; cacheControl is set only once the file opens, so a
// 404 for a file deleted behind the index is never cached.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path, cacheControl string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// imageCache picks how long browsers may keep an image or one of its
// renditions. Archived names embed the content's md5, so they never change;
// other files dropped in by hand might be replaced under the same name.
func (s *Server) imageCache(st *store.Store, name string) string {
	if it, ok := st.Get(name); ok && it.MD5 != "" {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=86400"
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	path, err := rt.Store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, path, s.imageCache(rt.Store, name))
}

func (s *Server) handleThumb(size store.Size) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, _, ok := s.resolveBoard(w, r, true)
		if !ok {
			return
		}
		path, err := rt.Store.Thumb(r.PathValue("name"), size)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			// Undecodable image: fall back to the original file.
			log.Printf("thumb: %v", err)
			s.handleImage(w, r)
			return
		}
		s.serveFile(w, r, path, s.imageCache(rt.Store, r.PathValue("name")))
	}
}

func attachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, true)
	if !ok {
		return
	}
	name := r.PathValue("name")
	path, err := rt.Store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	attachment(w, name)
	s.serveFile(w, r, path, "")
}

func (s *Server) handleZipSelected(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, true)
	if !ok {
		return
	}
	names := r.URL.Query()["f"]
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
			return
		}
		names = r.PostForm["f"]
	}
	seen := map[string]bool{}
	var items []store.Item
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		it, ok := rt.Store.Get(name)
		if !ok {
			writeError(w, http.StatusNotFound, "image_not_found", "图片不存在: "+name)
			return
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "no_selection", "未选择图片")
		return
	}
	if len(items) > GuestZipLimit && !s.auth.isAdmin(r) {
		writeError(w, http.StatusBadRequest, "guest_zip_limit", "访客一次最多打包下载 "+strconv.Itoa(GuestZipLimit)+" 张")
		return
	}
	if !checkZipFiles(w, rt.Store, items) {
		return
	}
	if checkOnly(w, r) {
		return
	}
	s.writeZip(w, r, rt.ID, rt.Store, "magma-"+rt.ID+"-selected-"+time.Now().Format(store.TimeLayout)+".zip", items)
}

func (s *Server) handleZipAll(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := s.resolveBoard(w, r, true)
	if !ok {
		return
	}
	items := rt.Store.All()
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "no_images", "暂无图片")
		return
	}
	if !checkZipFiles(w, rt.Store, items) {
		return
	}
	if checkOnly(w, r) {
		return
	}
	s.writeZip(w, r, rt.ID, rt.Store, "magma-"+rt.ID+"-all-"+time.Now().Format(store.TimeLayout)+".zip", items)
}

func checkZipFiles(w http.ResponseWriter, st *store.Store, items []store.Item) bool {
	for _, it := range items {
		info, err := os.Stat(filepath.Join(st.Dir(), it.Name))
		if err != nil || !info.Mode().IsRegular() {
			writeError(w, http.StatusNotFound, "image_not_found", "图片不存在: "+it.Name)
			return false
		}
	}
	return true
}

// checkOnly answers a ?check request once the archive would be served: the
// page asks first, so a refusal shows as a message instead of the browser
// saving the error as a file.
func checkOnly(w http.ResponseWriter, r *http.Request) bool {
	if !r.URL.Query().Has("check") {
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// writeZip streams the archive with uncompressed entries; images are already
// compressed, so deflate would only waste CPU. Once streaming has started a
// failure can only be signalled by cutting the connection: finishing the
// response normally would hand the browser a corrupt archive it calls complete.
func (s *Server) writeZip(w http.ResponseWriter, r *http.Request, boardID string, st *store.Store, filename string, items []store.Item) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	attachment(w, filename)
	zw := zip.NewWriter(w)
	for _, it := range items {
		if r.Context().Err() != nil {
			return // the client is gone
		}
		f, err := os.Open(filepath.Join(st.Dir(), it.Name))
		if err != nil {
			if _, _, resolveErr := s.boards.Resolve(boardID); resolveErr != nil {
				panic(http.ErrAbortHandler)
			}
			log.Printf("zip: open %s: %v", it.Name, err)
			panic(http.ErrAbortHandler)
		}
		err = addToZip(zw, f, it)
		f.Close()
		if err != nil {
			if r.Context().Err() != nil {
				return // the client went away mid-transfer
			}
			log.Printf("zip %s: %v", it.Name, err)
			panic(http.ErrAbortHandler)
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("zip close: %v", err)
		panic(http.ErrAbortHandler)
	}
}

func addToZip(zw *zip.Writer, f *os.File, it store.Item) error {
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: it.Name, Method: zip.Store, Modified: it.Time})
	if err != nil {
		return err
	}
	_, err = io.Copy(hw, f)
	return err
}
