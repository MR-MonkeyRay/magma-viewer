package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// asset is an embedded page file prepared once at startup: embedded files
// have no modification time, so a content hash serves as the validator.
type asset struct {
	ctype, cache string
	body, gz     []byte // gz is nil when compression does not pay off
	etag, gzEtag string
}

var compressible = map[string]bool{".html": true, ".js": true, ".css": true, ".svg": true, ".json": true, ".txt": true}

func loadAssets(static fs.FS) (map[string]asset, error) {
	assets := map[string]asset{}
	err := fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		tag := hex.EncodeToString(sum[:8])
		ext := path.Ext(p)
		a := asset{
			ctype: mime.TypeByExtension(ext),
			// Revalidated on every load (answered 304 when unchanged), so a release shows up at once.
			cache: "no-cache",
			body:  body,
			etag:  `"` + tag + `"`,
		}
		if a.ctype == "" {
			a.ctype = http.DetectContentType(body)
		}
		if strings.HasPrefix(p, "fonts/") {
			a.cache = "public, max-age=2592000" // 30 days: fonts change rarely

		}
		if compressible[ext] {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			zw.Write(body)
			zw.Close()
			if buf.Len() < len(body) {
				a.gz, a.gzEtag = buf.Bytes(), `"`+tag+`-gz"`
			}
		}
		assets[p] = a
		return nil
	})
	return assets, err
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	a, ok := s.assets[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Cache-Control", a.cache)
	body, etag := a.body, a.etag
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		// Ranges are served from the plain body: a multipart range reply cannot carry a gzip encoding.
		if acceptsGzip(r) && r.Header.Get("Range") == "" {
			body, etag = a.gz, a.gzEtag
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag) // ServeContent answers If-None-Match with 304
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		params := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(params[0]), "gzip") {
			continue
		}
		for _, p := range params[1:] {
			if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				return err == nil && q > 0
			}
		}
		return true
	}
	return false
}

var gzipPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression); return w }}

// gzipAPI compresses JSON from /api/ for clients that accept it; the
// overview with its 400 recent pictures shrinks to a fraction.
func gzipAPI(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || !acceptsGzip(r) {
			h.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		h.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	started bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.started {
		g.started = true
		hd := g.Header()
		hd.Add("Vary", "Accept-Encoding")
		if strings.HasPrefix(hd.Get("Content-Type"), "application/json") && hd.Get("Content-Encoding") == "" {
			hd.Set("Content-Encoding", "gzip")
			hd.Del("Content-Length")
			g.zw = gzipPool.Get().(*gzip.Writer)
			g.zw.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.started {
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) close() {
	if g.zw != nil {
		g.zw.Close()
		g.zw.Reset(io.Discard) // drop the reference to this response before pooling
		gzipPool.Put(g.zw)
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
