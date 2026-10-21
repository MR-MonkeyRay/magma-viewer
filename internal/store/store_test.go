package store

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func touch(t *testing.T, dir, name string, mod time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestIndexOrderAndPaging(t *testing.T) {
	dir := t.TempDir()
	sum := strings.Repeat("a", 32)
	touch(t, dir, "20261020-100000_"+sum+".png", time.Now())
	touch(t, dir, "20261020-120000_"+sum+".jpg", time.Now())
	touch(t, dir, "manual.webp", time.Date(2026, 10, 20, 11, 0, 0, 0, time.Local))
	touch(t, dir, "notes.txt", time.Now())
	touch(t, dir, ".hidden.png", time.Now())
	touch(t, dir, "x.png.part", time.Now())

	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 3 {
		t.Fatalf("count = %d, want 3", s.Count())
	}
	names := func(items []Item) []string {
		var out []string
		for _, it := range items {
			if it.MD5 == "" {
				out = append(out, it.Name)
			} else {
				out = append(out, it.Name[:8]+filepath.Ext(it.Name))
			}
		}
		return out
	}
	page1, total := s.List(1, 2, false)
	if total != 3 || strings.Join(names(page1), ",") != "20261020.jpg,manual.webp" {
		t.Fatalf("desc page1 = %v", names(page1))
	}
	page2, _ := s.List(2, 2, false)
	if strings.Join(names(page2), ",") != "20261020.png" {
		t.Fatalf("desc page2 = %v", names(page2))
	}
	asc, _ := s.List(1, 3, true)
	if names(asc)[0] != "20261020.png" {
		t.Fatalf("asc = %v", names(asc))
	}
	if out, _ := s.List(5, 2, false); len(out) != 0 {
		t.Fatal("out of range page should be empty")
	}

	latest, _ := s.Latest()
	if latest.MD5 != sum {
		t.Fatalf("md5 from name = %q", latest.MD5)
	}
}

func TestPathRejectsUnknown(t *testing.T) {
	s, _ := New(t.TempDir())
	for _, n := range []string{"../etc/passwd", "missing.png", ".thumbs"} {
		if _, err := s.Path(n); err != ErrNotFound {
			t.Fatalf("%q: err = %v", n, err)
		}
	}
}

func TestThumb(t *testing.T) {
	dir := t.TempDir()
	f, _ := os.Create(filepath.Join(dir, "big.png"))
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1000, 500)))
	f.Close()
	s, _ := New(dir)
	size := func(p string) (int, int) {
		tf, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer tf.Close()
		cfg, _, err := image.DecodeConfig(tf)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	p, err := s.Thumb("big.png", Small)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(p); w != 480 || h != 240 {
		t.Fatalf("small %dx%d", w, h)
	}
	// 1000px is within the preview edge, so the original is served.
	if p, _ := s.Thumb("big.png", Preview); p != filepath.Join(dir, "big.png") {
		t.Fatalf("preview path %s", p)
	}

	f, _ = os.Create(filepath.Join(dir, "huge.png"))
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2000, 4000)))
	f.Close()
	s.Rescan()
	p, err = s.Thumb("huge.png", Preview)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(p); w != 800 || h != 1600 {
		t.Fatalf("preview %dx%d", w, h)
	}
	if _, err := s.Thumb("missing.png", Small); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestScaleAveragesAndFlattens(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		src.Set(x, 0, color.NRGBA{255, 0, 0, 255}) // red row
		src.Set(x, 1, color.NRGBA{0, 0, 255, 0})   // fully transparent row
	}
	out := scale(src, 2) // 4x2 -> 2x1: each pixel averages red and white
	got := out.RGBAAt(0, 0)
	if got.R < 250 || got.G < 125 || got.G > 130 || got.B < 125 || got.B > 130 {
		t.Fatalf("got %+v, want ~{255 127 127}", got)
	}
}

func TestOverview(t *testing.T) {
	dir := t.TempDir()
	sum := strings.Repeat("b", 32)
	for _, ts := range []string{"20261019-230000", "20261020-080000", "20261020-200000", "20261021-070000"} {
		touch(t, dir, ts+"_"+sum+".png", time.Now())
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	o := s.Overview(24*time.Hour, 10, time.Local)
	if o.Count != 4 || o.Bytes != 4 {
		t.Fatalf("count, bytes = %d, %d", o.Count, o.Bytes)
	}
	if o.First.Format(TimeLayout) != "20261019-230000" || o.Last.Format(TimeLayout) != "20261021-070000" {
		t.Fatalf("span = %v .. %v", o.First, o.Last)
	}
	want := []struct {
		date  string
		count int
		hours map[int]int
	}{
		{"2026-10-21", 1, map[int]int{7: 1}},
		{"2026-10-20", 2, map[int]int{8: 1, 20: 1}},
		{"2026-10-19", 1, map[int]int{23: 1}},
	}
	if len(o.Days) != len(want) {
		t.Fatalf("days = %v", o.Days)
	}
	for i, w := range want {
		d := o.Days[i]
		if d.Date != w.date || d.Count != w.count {
			t.Fatalf("day %d = %s/%d, want %s/%d", i, d.Date, d.Count, w.date, w.count)
		}
		for h, n := range d.Hours {
			if n != w.hours[h] {
				t.Fatalf("%s hour %d = %d, want %d", d.Date, h, n, w.hours[h])
			}
		}
	}
	// 2026-10-20 08:00 is 23 h before the newest picture; 2026-10-19 23:00 is 32 h before.
	if len(o.Recent) != 3 || o.Recent[2].Time.Format(TimeLayout) != "20261020-080000" {
		t.Fatalf("recent = %v", o.Recent)
	}
	if got := s.Overview(24*time.Hour, 1, time.Local).Recent; len(got) != 1 {
		t.Fatalf("recent cap ignored: %d", len(got))
	}
	// Buckets follow the viewer's zone: 23:00 local moves to the next day 7 hours east.
	_, off := time.Date(2026, 10, 19, 23, 0, 0, 0, time.Local).Zone()
	east := time.FixedZone("east", off+7*3600)
	if d := s.Overview(time.Hour, 0, east).Days; d[len(d)-1].Date != "2026-10-20" || d[len(d)-1].Hours[6] != 1 {
		t.Fatalf("shifted days = %v", d)
	}

	empty, _ := New(t.TempDir())
	if o := empty.Overview(time.Hour, 10, time.Local); o.Count != 0 || o.Days == nil || o.Recent == nil {
		t.Fatalf("empty overview = %+v", o)
	}
}
