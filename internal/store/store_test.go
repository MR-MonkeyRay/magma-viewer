package store

import (
	"errors"
	"fmt"
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
	o := s.Overview(24*time.Hour, 4, 10, time.Local)
	if o.Count != 4 || o.Bytes != 4 {
		t.Fatalf("count, bytes = %d, %d", o.Count, o.Bytes)
	}
	if o.First.Format(TimeLayout) != "20261019-230000" || o.Last.Format(TimeLayout) != "20261021-070000" {
		t.Fatalf("span = %v .. %v", o.First, o.Last)
	}
	want := []DayCount{{"2026-10-21", 1}, {"2026-10-20", 2}, {"2026-10-19", 1}}
	if len(o.Days) != len(want) {
		t.Fatalf("days = %v", o.Days)
	}
	for i := range want {
		if o.Days[i] != want[i] {
			t.Fatalf("days = %v, want %v", o.Days, want)
		}
	}
	// 2026-10-20 08:00 is 23 h before the newest picture; 2026-10-19 23:00 is 32 h before.
	if len(o.Recent) != 3 || o.Recent[2].Time.Format(TimeLayout) != "20261020-080000" {
		t.Fatalf("recent = %v", o.Recent)
	}
	if got := s.Overview(24*time.Hour, 0, 1, time.Local).Recent; len(got) != 1 {
		t.Fatalf("recent cap ignored: %d", len(got))
	}
	// Six-hour slices from 20th 07:00: 08:00 falls in the first, 20:00 in the third, and the newest closes the last.
	// The recent cap leaves the heat counts alone.
	for _, limit := range []int{10, 1} {
		if h := s.Overview(24*time.Hour, 4, limit, time.Local).Heat; fmt.Sprint(h) != "[1 0 1 1]" {
			t.Fatalf("heat (cap %d) = %v", limit, h)
		}
	}
	// Days follow the viewer's zone: seven hours east, late pictures move to the next day.
	_, off := time.Date(2026, 10, 19, 23, 0, 0, 0, time.Local).Zone()
	east := time.FixedZone("east", off+7*3600)
	// 19th 23:00 -> 20th 06:00, 20th 08:00 -> 15:00, 20th 20:00 -> 21st 03:00, 21st 07:00 -> 14:00.
	if d := s.Overview(time.Hour, 0, 0, east).Days; len(d) != 2 || d[0] != (DayCount{"2026-10-21", 2}) || d[1] != (DayCount{"2026-10-20", 2}) {
		t.Fatalf("shifted days = %v", d)
	}

	empty, _ := New(t.TempDir())
	if o := empty.Overview(time.Hour, 4, 10, time.Local); o.Count != 0 || o.Days == nil || o.Recent == nil || len(o.Heat) != 4 {
		t.Fatalf("empty overview = %+v", o)
	}
}

func TestImportKeepsOrderWithoutRescan(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := func(c byte) string { return strings.Repeat(string(c), 32) }
	for _, name := range []string{"20261020-110000_" + sum('b') + ".png", "20261020-120000_" + sum('c') + ".png", "20261020-100000_" + sum('a') + ".png"} {
		if err := s.Import(strings.NewReader("x"), name); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, it := range s.All() {
		got = append(got, it.Name[9:15])
	}
	if strings.Join(got, ",") != "120000,110000,100000" {
		t.Fatalf("order after imports: %v", got)
	}
	// Watch compares the directory mtime with dirMod; our own writes must not look like a manual change.
	info, _ := os.Stat(dir)
	if !info.ModTime().Equal(s.dirMod) {
		t.Fatal("import left the directory looking changed, so Watch would rescan")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".state", "tmp")); len(entries) != 0 {
		t.Fatalf("leftover temp files: %v", entries)
	}
}

func TestImportNoticesEarlierManualChange(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A file dropped in by hand after the scan, then the directory mtime moves on.
	touch(t, dir, "manual.png", time.Now())
	past := time.Now().Add(-time.Hour)
	os.Chtimes(dir, past, past)
	if err := s.Import(strings.NewReader("x"), "20261020-100000_"+strings.Repeat("a", 32)+".png"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.ModTime().Equal(s.dirMod) {
		t.Fatal("import hid a manual change from Watch")
	}
}

func TestArchiveTimeZone(t *testing.T) {
	name := "20261020-120000_" + strings.Repeat("a", 32) + ".png"

	// A recorded zone wins over the process TZ.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".state"), 0o755)
	os.WriteFile(filepath.Join(dir, ".state", "archive.json"), []byte(`{"tz":"UTC"}`), 0o644)
	touch(t, dir, name, time.Now())
	t.Setenv("TZ", "Asia/Shanghai")
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.All()[0].Time; !got.Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("time parsed in the wrong zone: %v", got)
	}
	if got := s.FileName(time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC), "x", ".png"); got != "20261020-040000_x.png" {
		t.Fatalf("FileName ignores the archive zone: %s", got)
	}

	// The first run records the TZ in use.
	dir = t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".state", "archive.json")); !strings.Contains(string(data), "Asia/Shanghai") {
		t.Fatalf("archive zone not recorded: %s", data)
	}
}

func TestZoneName(t *testing.T) {
	cases := []struct {
		tz        string
		set       bool
		localtime string
		want      string
	}{
		{"Asia/Shanghai", true, "/usr/share/zoneinfo/America/Los_Angeles", "Asia/Shanghai"},
		{":Europe/Berlin", true, "", "Europe/Berlin"},
		{"", true, "/usr/share/zoneinfo/America/Los_Angeles", "UTC"}, // empty TZ means UTC to Go
		{"Local", true, "", ""},
		{"Not/AZone", true, "/usr/share/zoneinfo/Asia/Tokyo", ""},
		{"", false, "/usr/share/zoneinfo/Asia/Tokyo", "Asia/Tokyo"},
		{"", false, "../usr/share/zoneinfo/Europe/London", "Europe/London"},
		{"", false, "/etc/some-file", ""},
		{"", false, "", ""},
	}
	for _, c := range cases {
		if got := zoneName(c.tz, c.set, c.localtime); got != c.want {
			t.Errorf("zoneName(%q, %v, %q) = %q, want %q", c.tz, c.set, c.localtime, got, c.want)
		}
	}
}

func TestDamagedArchiveZoneStopsStartup(t *testing.T) {
	for _, content := range []string{"{not json", `{"tz":""}`, `{"tz":"Not/AZone"}`, `{"tz":"Local"}`} {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".state"), 0o755)
		os.WriteFile(filepath.Join(dir, ".state", "archive.json"), []byte(content), 0o644)
		if _, err := New(dir); err == nil {
			t.Errorf("archive.json %q accepted", content)
		}
	}
}

func TestImportDuringRescanIsNotLost(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			s.Rescan()
		}
	}()
	for i := range 50 {
		name := fmt.Sprintf("20261020-1200%02d_%032d.png", i, i)
		if err := s.Import(strings.NewReader("x"), name); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Get(name); !ok {
			t.Fatalf("%s vanished from the index right after import", name)
		}
	}
	<-done
	if s.Count() != 50 {
		t.Fatalf("count %d after concurrent rescans", s.Count())
	}
}

func TestPixelReaderFastPathsMatchGeneric(t *testing.T) {
	r := image.Rect(0, 0, 3, 2)
	nrgba := image.NewNRGBA(r)
	gray := image.NewGray(r)
	pal := image.NewPaletted(r, color.Palette{color.NRGBA{10, 200, 30, 255}, color.NRGBA{255, 0, 0, 128}, color.Transparent})
	for i, c := range []color.NRGBA{{255, 0, 0, 255}, {0, 128, 255, 128}, {9, 9, 9, 0}, {200, 100, 50, 77}, {1, 2, 3, 254}, {0, 0, 0, 255}} {
		x, y := i%3, i/3
		nrgba.SetNRGBA(x, y, c)
		gray.SetGray(x, y, color.Gray{uint8(i * 40)})
		pal.SetColorIndex(x, y, uint8(i%3))
	}
	// The generic path wraps the image so the type switch cannot see its concrete type.
	type opaque struct{ image.Image }
	for name, img := range map[string]image.Image{"nrgba": nrgba, "gray": gray, "paletted": pal} {
		fast, slow := pixelReader(img), pixelReader(opaque{img})
		for y := 0; y < 2; y++ {
			for x := 0; x < 3; x++ {
				fr, fg, fb := fast(x, y)
				sr, sg, sb := slow(x, y)
				// Rounding differs between 8- and 16-bit arithmetic by at most one step.
				if absDiff(fr, sr) > 1 || absDiff(fg, sg) > 1 || absDiff(fb, sb) > 1 {
					t.Errorf("%s (%d,%d): fast %d,%d,%d slow %d,%d,%d", name, x, y, fr, fg, fb, sr, sg, sb)
				}
			}
		}
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestUndecodableImageIsNotRetriedUntilChanged(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "broken.png", time.Now().Add(-time.Hour))
	s, _ := New(dir)
	for range 2 {
		if _, err := s.Thumb("broken.png", Small); !errors.Is(err, errUndecodable) {
			t.Fatalf("broken image: %v", err)
		}
	}
	if _, ok := s.undecoded.Load("broken.png"); !ok {
		t.Fatal("failure not remembered")
	}
	// Replacing the file lifts the verdict, even if a copy tool kept the old mtime.
	p := filepath.Join(dir, "broken.png")
	before, _ := os.Stat(p)
	f, _ := os.Create(p)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 600, 300)))
	f.Close()
	os.Chtimes(p, before.ModTime(), before.ModTime())
	os.Chtimes(filepath.Join(dir, ".thumbs"), before.ModTime(), before.ModTime())
	if _, err := s.Thumb("broken.png", Small); err != nil {
		t.Fatalf("after repair: %v", err)
	}
	if _, ok := s.undecoded.Load("broken.png"); ok {
		t.Fatal("stale failure kept after a good render")
	}
	n := 0
	s.thumbLocks.Range(func(any, any) bool { n++; return true })
	if n != 0 {
		t.Fatalf("%d render locks kept after use", n)
	}
}

func TestCleanupRemovesStaleLeftoversOnly(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	keep := "20261020-100000_" + strings.Repeat("a", 32) + ".png"
	touch(t, dir, keep, time.Now())
	s, _ := New(dir)
	old, recent := time.Now().Add(-2*time.Hour), time.Now()
	stale := []string{
		filepath.Join(dir, keep+".part"),
		filepath.Join(dir, ".state", "fetcher.json.part"),
		filepath.Join(dir, ".state", "tmp", ".import.abc.part"),
		filepath.Join(dir, ".thumbs", ".thumb-123"),
		filepath.Join(tmp, "magma-1.download"),
		filepath.Join(dir, ".thumbs", "gone.png.jpg"),   // orphaned thumbnail
		filepath.Join(dir, ".thumbs", "gone.png.m.jpg"), // orphaned preview
	}
	touch(t, dir, "fresh-copy.png", old) // on disk but not indexed yet
	kept := []string{
		filepath.Join(dir, ".thumbs", "fresh-copy.png.jpg"),
		filepath.Join(dir, ".thumbs", keep+".jpg"),
		filepath.Join(dir, ".thumbs", keep+".m.jpg"),
		filepath.Join(tmp, "magma-2.download"), // possibly still being written
		filepath.Join(tmp, "unrelated.txt"),
	}
	for _, p := range append(stale, kept...) {
		touch(t, filepath.Dir(p), filepath.Base(p), old)
	}
	os.Chtimes(kept[3], recent, recent)
	s.Cleanup(tmp)
	for _, p := range stale {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived cleanup", p)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
}

func TestGenerationChangesWithIndex(t *testing.T) {
	s, _ := New(t.TempDir())
	g := s.Generation()
	s.Import(strings.NewReader("x"), "20261020-100000_"+strings.Repeat("a", 32)+".png")
	if s.Generation() == g {
		t.Fatal("generation unchanged after import")
	}
}
