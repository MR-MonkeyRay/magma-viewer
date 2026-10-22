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
	store   *store.Store
	fetcher *fetcher.Fetcher
	notices *notice.Store
	auth    *auth
	assets  map[string]asset // embedded page files by path

	zones    sync.Map // tz name -> *time.Location
	overview struct {
		mu     sync.Mutex
		gen    uint64
		byZone map[string][]byte // encoded responses for gen
	}

	// NextRun reports when the scheduler fires next; nil leaves next_run unset.
	NextRun func() time.Time
	// Version is the release shown in the page footer.
	Version string
	// Notifier sends test alerts from the admin page; nil when no webhook is configured.
	Notifier *notify.Notifier
}

// Go's built-in table lacks woff2, and slim images ship no /etc/mime.types.
func init() { _ = mime.AddExtensionType(".woff2", "font/woff2") }

func New(cfg *config.Config, st *store.Store, f *fetcher.Fetcher, ns *notice.Store, static fs.FS) *Server {
	a := newAuth(cfg.Admin.Username, cfg.Admin.Password, cfg.Admin.SessionSecret, cfg.Admin.SessionTTL)
	a.proxies = cfg.TrustedProxyPrefixes()
	assets, err := loadAssets(static)
	if err != nil {
		log.Printf("web: %v", err) // only an unreadable embedded FS gets here
	}
	return &Server{
		cfg:     cfg,
		store:   st,
		fetcher: f,
		notices: ns,
		auth:    a,
		assets:  assets,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleAsset)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /readyz", s.handleReady)

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
	size := min(max(intParam(r, "size", defaultPageSize), minPageSize), maxPageSize)
	asc := r.URL.Query().Get("sort") == "asc"
	// around= or date= ask for the page holding that picture or day instead.
	items, total, page, focus := s.store.ListAt(intParam(r, "page", 1), size, asc, s.locator(r, asc))
	task := s.fetcher.Status()
	pages := max((total+size-1)/size, 1)
	writeJSON(w, http.StatusOK, map[string]any{
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
			"paused": task.Paused, "last_fetched": task.LastFetched, "next_run": s.nextRun(),
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
	writeCachedJSON(w, s.overviewJSON(s.zone(r.URL.Query().Get("tz"))))
}

// overviewJSON encodes the overview for loc, reusing it until the index changes.
func (s *Server) overviewJSON(loc *time.Location) []byte {
	gen := s.store.Generation()
	c := &s.overview
	c.mu.Lock()
	if c.gen != gen || c.byZone == nil {
		c.gen, c.byZone = gen, map[string][]byte{}
	}
	body, ok := c.byZone[loc.String()]
	c.mu.Unlock()
	if ok {
		return body
	}
	// 96 quarter hours feed the reel's heat strip; playback is capped at the newest 400 pictures.
	body, _ = json.Marshal(s.store.Overview(24*time.Hour, 96, 400, loc))
	c.mu.Lock()
	if c.gen == gen {
		c.byZone[loc.String()] = body
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

func (s *Server) nextRun() time.Time {
	if s.NextRun == nil {
		return time.Time{}
	}
	return s.NextRun()
}

// disk is the space around the archive.
type disk struct {
	Free    int64 `json:"free"`
	Total   int64 `json:"total"`
	Archive int64 `json:"archive"` // bytes taken by the indexed pictures
}

func (s *Server) statusBody() map[string]any {
	d := diskSpace(s.store.Dir())
	if d != nil {
		d.Archive = s.store.Bytes()
	}
	return map[string]any{
		"fetch":    s.fetcher.Status(),
		"token":    s.fetcher.Token(),
		"cookie":   s.fetcher.Cookie(),
		"history":  s.fetcher.History(),
		"notify":   s.Notifier != nil,
		"disk":     d,
		"schedule": s.cfg.Schedule,
		"next_run": s.nextRun(),
		"count":    s.store.Count(),
	}
}

// handleReady is for monitoring: 503 when pictures could not be saved.
// Fetch health is reported but does not fail the check, since an expired
// cookie is not something restarting the service would fix.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	st := s.fetcher.Status()
	err := s.store.Writable()
	code := http.StatusOK
	if err != nil {
		log.Printf("readyz: storage not writable: %v", err)
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"ok":               err == nil,
		"storage_writable": err == nil,
		"last_fetched":     st.LastFetched,
		"failures":         st.Failures,
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

func (s *Server) handleCookie(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cookie string `json:"cookie"`
	}
	if !readJSON(w, r, 16<<10, &body) {
		return
	}
	if err := s.fetcher.SetCookie(body.Cookie); err != nil {
		if errors.Is(err, fetcher.ErrInvalidCookie) {
			writeError(w, http.StatusBadRequest, "invalid_cookie", "Cookie 格式不正确")
			return
		}
		log.Printf("cookie: %v", err)
		writeError(w, http.StatusInternalServerError, "cookie_save_failed", "保存 Cookie 失败")
		return
	}
	writeJSON(w, http.StatusOK, s.statusBody())
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statusBody())
}

func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Magma.Timeout+10*time.Second)
	defer cancel()
	s.fetcher.Run(ctx)
	writeJSON(w, http.StatusOK, s.statusBody())
}

// readJSON decodes a JSON request body of at most limit bytes into v, answering the error itself.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	// A cross-origin form cannot send JSON without a CORS preflight.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request", "请求格式错误")
		return false
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, limit)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求格式错误")
		return false
	}
	return true
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
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
	if err := s.fetcher.SetPaused(*body.Paused); err != nil {
		log.Printf("schedule: %v", err)
		writeError(w, http.StatusInternalServerError, "schedule_save_failed", "保存定时任务状态失败")
		return
	}
	writeJSON(w, http.StatusOK, s.statusBody())
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
func (s *Server) imageCache(name string) string {
	if it, ok := s.store.Get(name); ok && it.MD5 != "" {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=86400"
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, path, s.imageCache(name))
}

func (s *Server) handleThumb(size store.Size) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := s.store.Thumb(r.PathValue("name"), size)
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
		s.serveFile(w, r, path, s.imageCache(r.PathValue("name")))
	}
}

func attachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	attachment(w, name)
	s.serveFile(w, r, path, "")
}

func (s *Server) handleZipSelected(w http.ResponseWriter, r *http.Request) {
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
		it, ok := s.store.Get(name)
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
	if checkOnly(w, r) {
		return
	}
	s.writeZip(w, r, "magma-selected-"+time.Now().Format(store.TimeLayout)+".zip", items)
}

func (s *Server) handleZipAll(w http.ResponseWriter, r *http.Request) {
	items := s.store.All()
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "no_images", "暂无图片")
		return
	}
	if checkOnly(w, r) {
		return
	}
	s.writeZip(w, r, "magma-all-"+time.Now().Format(store.TimeLayout)+".zip", items)
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
func (s *Server) writeZip(w http.ResponseWriter, r *http.Request, filename string, items []store.Item) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	attachment(w, filename)
	zw := zip.NewWriter(w)
	for _, it := range items {
		if r.Context().Err() != nil {
			return // the client is gone
		}
		f, err := os.Open(filepath.Join(s.store.Dir(), it.Name))
		if err != nil {
			// Deleted by hand since the last rescan: leave it out rather than fail the archive.
			log.Printf("zip: skip %s: %v", it.Name, err)
			continue
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
