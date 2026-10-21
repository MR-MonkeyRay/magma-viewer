// Package store indexes the image directory in memory and generates cached
// thumbnails.
package store

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

// TimeLayout is the timestamp prefix of stored file names.
const TimeLayout = "20060102-150405"

var (
	ErrNotFound = errors.New("image not found")

	namePattern = regexp.MustCompile(`^(\d{8}-\d{6})_([0-9a-f]{32})\.[a-z]+$`)

	allowedExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}
)

type Item struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
	W    int       `json:"w,omitempty"` // pixel size, 0 when the header is unreadable
	H    int       `json:"h,omitempty"`
	MD5  string    `json:"-"` // only known when encoded in the file name
	mod  time.Time // tie-breaker for items saved within the same second
}

type Store struct {
	dir      string
	thumbDir string

	mu     sync.RWMutex
	items  []Item // newest first
	byName map[string]int
	dirMod time.Time

	thumbSem   chan struct{}
	thumbLocks sync.Map // name -> *sync.Mutex
}

func New(dir string) (*Store, error) {
	s := &Store{
		dir:      dir,
		thumbDir: filepath.Join(dir, ".thumbs"),
		// Decoding a 4K image needs ~35 MB, so keep concurrency low.
		thumbSem: make(chan struct{}, min(2, runtime.NumCPU())),
	}
	if err := os.MkdirAll(s.thumbDir, 0o755); err != nil {
		return nil, err
	}
	if err := s.Rescan(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Dir() string { return s.dir }

// FileName builds the canonical stored name for an image.
func FileName(t time.Time, sum, ext string) string {
	return t.Format(TimeLayout) + "_" + sum + ext
}

// IsAllowedExt reports whether ext (with dot) is a supported image extension.
func IsAllowedExt(ext string) bool { return allowedExt[strings.ToLower(ext)] }

// imageSize reads only the image header, so it is cheap even for large files.
func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func parseItem(name string, info os.FileInfo) Item {
	it := Item{Name: name, Size: info.Size(), Time: info.ModTime(), mod: info.ModTime()}
	if m := namePattern.FindStringSubmatch(name); m != nil {
		if t, err := time.ParseInLocation(TimeLayout, m[1], time.Local); err == nil {
			it.Time = t
		}
		it.MD5 = m[2]
	}
	return it
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Time.Equal(items[j].Time) {
			return items[i].Time.After(items[j].Time)
		}
		if !items[i].mod.Equal(items[j].mod) {
			return items[i].mod.After(items[j].mod)
		}
		return items[i].Name > items[j].Name
	})
}

func (s *Store) reindex(items []Item) {
	sortItems(items)
	byName := make(map[string]int, len(items))
	for i, it := range items {
		byName[it.Name] = i
	}
	s.items, s.byName = items, byName
}

// Rescan rebuilds the index from the directory contents.
func (s *Store) Rescan() error {
	dirInfo, err := os.Stat(s.dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	// Reuse known dimensions so a rescan does not reopen every file.
	s.mu.RLock()
	prev := make(map[string]Item, len(s.items))
	for _, it := range s.items {
		prev[it.Name] = it
	}
	s.mu.RUnlock()

	items := make([]Item, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !IsAllowedExt(filepath.Ext(name)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := parseItem(name, info)
		if p, ok := prev[name]; ok && p.Size == it.Size && p.mod.Equal(it.mod) {
			it.W, it.H = p.W, p.H
		} else {
			it.W, it.H = imageSize(filepath.Join(s.dir, name))
		}
		items = append(items, it)
	}
	s.mu.Lock()
	s.reindex(items)
	s.dirMod = dirInfo.ModTime()
	s.mu.Unlock()
	return nil
}

// Add indexes a file that was just written into the directory.
func (s *Store) Add(name string) error {
	info, err := os.Stat(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	it := parseItem(name, info)
	it.W, it.H = imageSize(filepath.Join(s.dir, name))
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items
	if i, ok := s.byName[name]; ok {
		items[i] = it
	} else {
		items = append(items, it)
	}
	s.reindex(items)
	return nil
}

// Watch rescans when the directory mtime changes, picking up manual edits.
func (s *Store) Watch(stop <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			info, err := os.Stat(s.dir)
			if err != nil {
				continue
			}
			s.mu.RLock()
			changed := !info.ModTime().Equal(s.dirMod)
			s.mu.RUnlock()
			if changed {
				if err := s.Rescan(); err != nil {
					log.Printf("store: rescan: %v", err)
				}
			}
		}
	}
}

// List returns one page of items and the total count.
func (s *Store) List(page, size int, asc bool) ([]Item, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.items)
	start := (page - 1) * size
	if start < 0 || start >= total {
		return []Item{}, total
	}
	end := min(start+size, total)
	out := make([]Item, 0, end-start)
	for i := start; i < end; i++ {
		idx := i
		if asc {
			idx = total - 1 - i
		}
		out = append(out, s.items[idx])
	}
	return out, total
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// All returns a snapshot of every item, newest first.
func (s *Store) All() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Item(nil), s.items...)
}

func (s *Store) Get(name string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byName[name]
	if !ok {
		return Item{}, false
	}
	return s.items[i], true
}

func (s *Store) Latest() (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.items) == 0 {
		return Item{}, false
	}
	return s.items[0], true
}

// Path returns the on-disk path for an indexed name, which also rules out
// path traversal.
func (s *Store) Path(name string) (string, error) {
	if _, ok := s.Get(name); !ok {
		return "", ErrNotFound
	}
	return filepath.Join(s.dir, name), nil
}

// FileMD5 returns the md5 of an item, computing it when the name lacks one.
func (s *Store) FileMD5(it Item) (string, error) {
	if it.MD5 != "" {
		return it.MD5, nil
	}
	f, err := os.Open(filepath.Join(s.dir, it.Name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Size selects a cached rendition of an image.
type Size int

const (
	Small   Size = iota // grid thumbnail
	Preview             // lightbox / hero, much lighter than a 4K original
)

var sizeEdge = map[Size]int{Small: 480, Preview: 1600}

func (s *Store) cachePath(name string, size Size) string {
	if size == Preview {
		return filepath.Join(s.thumbDir, name+".m.jpg")
	}
	return filepath.Join(s.thumbDir, name+".jpg")
}

// Thumb returns a cached JPEG, generating it if needed. For Preview, GIFs and
// images within 1600px are served as-is.
func (s *Store) Thumb(name string, size Size) (string, error) {
	it, ok := s.Get(name)
	if !ok {
		return "", ErrNotFound
	}
	src := filepath.Join(s.dir, name)
	if size == Preview && (strings.EqualFold(filepath.Ext(name), ".gif") ||
		(it.W > 0 && max(it.W, it.H) <= sizeEdge[Preview])) {
		return src, nil
	}
	dst := s.cachePath(name, size)
	if fresh(dst, src) {
		return dst, nil
	}
	if err := s.Ensure(name); err != nil {
		return "", err
	}
	return dst, nil
}

// Ensure generates all missing renditions of name with a single decode.
func (s *Store) Ensure(name string) error {
	src := filepath.Join(s.dir, name)
	small, preview := s.cachePath(name, Small), s.cachePath(name, Preview)
	if fresh(small, src) && fresh(preview, src) {
		return nil
	}

	l, _ := s.thumbLocks.LoadOrStore(name, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if fresh(small, src) && fresh(preview, src) {
		return nil
	}

	s.thumbSem <- struct{}{}
	defer func() { <-s.thumbSem }()
	if err := makeThumbs(src, preview, small); err != nil {
		return fmt.Errorf("thumbnail %s: %w", name, err)
	}
	return nil
}

// Warm generates missing renditions one at a time at startup.
func (s *Store) Warm(stop <-chan struct{}) {
	n := 0
	for _, it := range s.All() {
		select {
		case <-stop:
			return
		default:
		}
		src := filepath.Join(s.dir, it.Name)
		if fresh(s.cachePath(it.Name, Small), src) && fresh(s.cachePath(it.Name, Preview), src) {
			continue
		}
		if err := s.Ensure(it.Name); err != nil {
			log.Printf("store: %v", err)
			continue
		}
		n++
	}
	if n > 0 {
		log.Printf("store: generated thumbnails for %d images", n)
	}
}

func fresh(dst, src string) bool {
	d, err := os.Stat(dst)
	if err != nil {
		return false
	}
	sInfo, err := os.Stat(src)
	return err == nil && !d.ModTime().Before(sInfo.ModTime())
}

func fit(w, h, edge int) (int, int) {
	if w <= edge && h <= edge {
		return w, h
	}
	if w >= h {
		return edge, max(1, h*edge/w)
	}
	return max(1, w*edge/h), edge
}

// scale downsamples to fit within edge by area averaging, flattening
// transparency onto white. Unlike x/image/draw kernels, it keeps one row of
// accumulators instead of allocating 32 bytes per destination/source pixel.
func scale(img image.Image, edge int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := fit(sw, sh, edge)
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	pixel := pixelReader(img)

	// xs[dx]..xs[dx+1] is the source column range of destination column dx.
	xs := make([]int, dw+1)
	for dx := range xs {
		xs[dx] = dx * sw / dw
	}
	acc := make([][4]uint32, dw) // r, g, b, count
	for dy := 0; dy < dh; dy++ {
		clear(acc)
		for sy := dy * sh / dh; sy < (dy+1)*sh/dh; sy++ {
			dx := 0
			for sx := 0; sx < sw; sx++ {
				for sx >= xs[dx+1] {
					dx++
				}
				r, g, bl := pixel(b.Min.X+sx, b.Min.Y+sy)
				a := &acc[dx]
				a[0] += uint32(r)
				a[1] += uint32(g)
				a[2] += uint32(bl)
				a[3]++
			}
		}
		row := out.Pix[dy*out.Stride:]
		for dx, a := range acc {
			n := max(a[3], 1)
			row[dx*4+0] = uint8(a[0] / n)
			row[dx*4+1] = uint8(a[1] / n)
			row[dx*4+2] = uint8(a[2] / n)
			row[dx*4+3] = 0xff
		}
	}
	return out
}

// pixelReader returns an 8-bit RGB accessor composited onto white, with fast
// paths for JPEG and PNG decoder types.
func pixelReader(img image.Image) func(x, y int) (uint8, uint8, uint8) {
	switch m := img.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint8, uint8, uint8) {
			ci := m.COffset(x, y)
			return color.YCbCrToRGB(m.Y[m.YOffset(x, y)], m.Cb[ci], m.Cr[ci])
		}
	case *image.RGBA: // premultiplied
		return func(x, y int) (uint8, uint8, uint8) {
			p := m.Pix[m.PixOffset(x, y):]
			w := 0xff - p[3]
			return p[0] + w, p[1] + w, p[2] + w
		}
	default:
		return func(x, y int) (uint8, uint8, uint8) {
			r, g, b, a := m.At(x, y).RGBA()
			w := 0xffff - a
			return uint8((r + w) >> 8), uint8((g + w) >> 8), uint8((b + w) >> 8)
		}
	}
}

func makeThumbs(src, previewDst, smallDst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	preview := scale(img, sizeEdge[Preview])
	img = nil // let the full-size decode be collected before encoding
	if err := writeJPEG(previewDst, preview, 85); err != nil {
		return err
	}
	// Downscaling from the preview is much cheaper than from the original.
	return writeJPEG(smallDst, scale(preview, sizeEdge[Small]), 80)
}

func writeJPEG(dst string, img image.Image, quality int) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".thumb-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: quality}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // CreateTemp uses 0600
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
