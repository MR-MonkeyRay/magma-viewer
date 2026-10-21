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
	"os"
	"path/filepath"
	"strconv"
	"time"

	"magma-viewer/internal/config"
	"magma-viewer/internal/fetcher"
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
	auth    *auth
	static  fs.FS
}

func New(cfg *config.Config, st *store.Store, f *fetcher.Fetcher, static fs.FS) *Server {
	return &Server{
		cfg:     cfg,
		store:   st,
		fetcher: f,
		auth:    newAuth(cfg.Admin.Username, cfg.Admin.Password, cfg.Admin.SessionSecret, cfg.Admin.SessionTTL),
		static:  static,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	staticFS := http.FileServerFS(s.static)
	mux.Handle("GET /", noCache(staticFS))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })

	mux.HandleFunc("GET /api/images", s.handleList)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/status", s.adminOnly(s.handleStatus))
	mux.HandleFunc("POST /api/fetch", s.adminOnly(s.handleFetch))

	mux.HandleFunc("GET /img/{name}", s.handleImage)
	mux.HandleFunc("GET /thumb/{name}", s.handleThumb(store.Small))
	mux.HandleFunc("GET /preview/{name}", s.handleThumb(store.Preview))
	mux.HandleFunc("GET /download/zip", s.handleZipSelected)
	mux.HandleFunc("GET /download/all", s.adminOnly(s.handleZipAll))
	mux.HandleFunc("GET /download/{name}", s.handleDownload)
	return logRequests(mux)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.isAdmin(r) {
			writeError(w, http.StatusUnauthorized, "需要管理员登录")
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
	page := max(intParam(r, "page", 1), 1)
	items, total := s.store.List(page, size, asc)
	pages := max((total+size-1)/size, 1)
	if page > pages {
		page = pages
		items, _ = s.store.List(page, size, asc)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"total":       total,
		"page":        page,
		"size":        size,
		"pages":       pages,
		"admin":       s.auth.isAdmin(r),
		"guest_limit": GuestZipLimit,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.auth.allowAttempt(ip) {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !s.auth.checkCredentials(body.Username, body.Password) {
		s.auth.recordFailure(ip)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
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

func (s *Server) statusBody() map[string]any {
	return map[string]any{
		"fetch":    s.fetcher.Status(),
		"token":    s.fetcher.Token(),
		"schedule": s.cfg.Schedule,
		"count":    s.store.Count(),
	}
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

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
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
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	path, err := s.store.Path(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	s.serveFile(w, r, path, "")
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
		w.Header().Set("Cache-Control", "public, max-age=86400")
		s.serveFile(w, r, path, "")
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
	seen := map[string]bool{}
	var items []store.Item
	for _, name := range r.URL.Query()["f"] {
		if seen[name] {
			continue
		}
		seen[name] = true
		it, ok := s.store.Get(name)
		if !ok {
			writeError(w, http.StatusNotFound, "图片不存在: "+name)
			return
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "未选择图片")
		return
	}
	if len(items) > GuestZipLimit && !s.auth.isAdmin(r) {
		writeError(w, http.StatusBadRequest, "访客一次最多打包下载 "+strconv.Itoa(GuestZipLimit)+" 张")
		return
	}
	s.writeZip(w, r, "magma-selected-"+time.Now().Format(store.TimeLayout)+".zip", items)
}

func (s *Server) handleZipAll(w http.ResponseWriter, r *http.Request) {
	items := s.store.All()
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "暂无图片")
		return
	}
	s.writeZip(w, r, "magma-all-"+time.Now().Format(store.TimeLayout)+".zip", items)
}

// writeZip streams the archive. Images are already compressed, so entries are
// stored without deflate to keep CPU usage low.
func (s *Server) writeZip(w http.ResponseWriter, r *http.Request, filename string, items []store.Item) {
	w.Header().Set("Content-Type", "application/zip")
	attachment(w, filename)
	zw := zip.NewWriter(w)
	for _, it := range items {
		if r.Context().Err() != nil {
			return
		}
		if err := addToZip(zw, filepath.Join(s.store.Dir(), it.Name), it); err != nil {
			log.Printf("zip %s: %v", it.Name, err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("zip close: %v", err)
	}
}

func addToZip(zw *zip.Writer, path string, it store.Item) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: it.Name, Method: zip.Store, Modified: it.Time})
	if err != nil {
		return err
	}
	_, err = io.Copy(hw, f)
	return err
}
