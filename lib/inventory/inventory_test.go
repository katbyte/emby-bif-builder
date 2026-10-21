package inventory_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/paths"
	"github.com/katbyte/emby-bif-builder/lib/servertest"
	"golang.org/x/text/unicode/norm"
)

// take reads everything the made-up servers have.
func take(t *testing.T, s *servertest.Servers, libraries ...string) *inventory.Inventory {
	t.Helper()

	s.Done()
	e, err := emby.Open(t.Context(), s.EmbyDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	j, err := jellyfin.Open(t.Context(), s.JellyfinDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	m, err := paths.Parse(s.MediaPaths())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	ended := map[inventory.Step]int{}
	progress := func(ev inventory.Event) {
		mu.Lock()
		defer mu.Unlock()

		if ev.Ended {
			ended[ev.Step]++
		}
	}

	inv, err := inventory.Take(t.Context(), inventory.Options{Emby: e, Jellyfin: j, Paths: m, Libraries: libraries, Workers: 2, Progress: progress})
	if err != nil {
		t.Fatal(err)
	}
	if ended[inventory.EmbyVideos] != 1 || ended[inventory.JellyfinThumbnails] != 1 || ended[inventory.Folders] != 1 {
		t.Errorf("the steps ended %v times, want each once", ended)
	}

	return inv
}

// video is the video at this path in the inventory.
func video(t *testing.T, inv *inventory.Inventory, path string) *inventory.Video {
	t.Helper()

	for _, v := range inv.Videos {
		if v.Path == path {
			return v
		}
	}
	t.Fatalf("%s is not in the inventory", path)

	return nil
}

// Each BIF beside a video is found as that video's, however it is named, and a BIF of another name, or a hidden file, is no video's.
func TestTheBIFsBesideEachVideo(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Library("TV", "tvshows", "tv")
	plain := s.Episode("tv/Show/Show - 01x01.mkv")
	s.BIF(plain, "Show - 01x01-320-10.bif", 32, 18, 3)
	s.BIF(plain, "Show - 01x01.bif", 16, 9, 3)
	s.BIF(plain, ".Show - 01x01-320-10.bif.123.ebb-tmp", 16, 9, 3)
	s.BIF(plain, "Show - 01x01 - extended-320-10.bif", 16, 9, 3)

	// a video named as if its name gave a width and seconds is still found by its bare BIF
	dated := s.Episode("tv/Show/Show - 2020-10-05.mp4")
	s.BIF(dated, "Show - 2020-10-05.bif", 16, 9, 3)

	// two videos of one name share its BIF
	other := s.Episode("tv/Show/Show - 01x01.avi")

	broken := s.Episode("tv/Show/Show - 01x02.mkv")
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.Local(broken)), "Show - 01x02-320-10.bif"), []byte("not a bif"), 0o600); err != nil {
		t.Fatal(err)
	}

	inv := take(t, s)

	v := video(t, inv, plain)
	if len(v.BIFs) != 2 || !v.Found || v.Library != "TV" || v.Local != s.Local(plain) {
		t.Fatalf("the plain video is %+v, want its two BIFs", v)
	}
	if b, ok := v.Widest(); !ok || b.Style != inventory.Sized || b.Named != "320-10" || b.NamedWidth() != 320 || b.Info.Width != 32 || b.Info.Frames != 3 || b.Bytes == 0 {
		t.Errorf("its widest BIF is %+v", b)
	}

	if d := video(t, inv, dated); len(d.BIFs) != 1 || d.BIFs[0].Style != inventory.Bare || d.BIFs[0].NamedWidth() != 0 {
		t.Errorf("the dated video's BIFs are %+v, want its bare one", d.BIFs)
	}
	if o := video(t, inv, other); len(o.BIFs) != 2 {
		t.Errorf("the video of the same name has %d BIFs, want the two it shares", len(o.BIFs))
	}

	b := video(t, inv, broken)
	if len(b.BIFs) != 1 || b.BIFs[0].Err == nil {
		t.Errorf("the broken BIF is %+v, want it found and said to be unreadable", b.BIFs)
	}
	if _, ok := b.Widest(); ok {
		t.Error("a video whose only BIF cannot be read has a widest one")
	}
}

// A video and its BIF are found when the folder gives their names back with each accent apart from its letter, as a Mac's share does, and Emby
// has them with the two as one.
func TestAccentedNames(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Library("Docs", "movies", "docs")
	composed := s.Film("docs/Ghosts of Cit\u00e9 Soleil (2006)/Ghosts of Cit\u00e9 Soleil (2006).mp4")

	// the file and its BIF are named as a Mac's share gives the names back
	dir := filepath.Dir(s.Local(composed))
	if err := os.Rename(s.Local(composed), filepath.Join(dir, norm.NFD.String(filepath.Base(composed)))); err != nil {
		t.Fatal(err)
	}
	s.BIF(composed, norm.NFD.String("Ghosts of Cit\u00e9 Soleil (2006)-320-10.bif"), 32, 18, 3)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Skipf("this filesystem keeps one of the two names: %v", entries)
	}

	v := video(t, take(t, s), composed)
	if !v.Found || len(v.BIFs) != 1 {
		t.Errorf("the accented film is %+v, want it found with its BIF", v)
	}
}

// A video whose file is not there, or whose folder is not, is said to be so.
func TestVideosThatAreNotThere(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Library("TV", "tvshows", "tv")
	gone := s.Episode("tv/Show/Show - 01x01.mkv")
	if err := os.Remove(s.Local(gone)); err != nil {
		t.Fatal(err)
	}
	noFolder := s.Episode("tv/Other/Other - 01x01.mkv")
	if err := os.RemoveAll(filepath.Dir(s.Local(noFolder))); err != nil {
		t.Fatal(err)
	}

	inv := take(t, s)
	if v := video(t, inv, gone); v.Found || v.Err != nil {
		t.Errorf("the gone video is %+v, want it not found", v)
	}
	if v := video(t, inv, noFolder); v.Found || v.Err == nil {
		t.Errorf("the video with no folder is %+v, want its folder said to be unreadable", v)
	}
}

// Only the libraries named are read, whatever their case, and the videos Jellyfin has thumbnails of are matched to Emby's, the rest in those
// libraries' folders counted apart.
func TestWhatIsTaken(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Library("TV", "tvshows", "tv")
	s.Library("Movies", "movies", "movies")
	s.Show("tv/Show")
	s.Thumbnails(s.Episode("tv/Show/Show - 01x01.mkv"), 32, 18, 3)
	s.Film("movies/Film/Film.mkv")
	s.Thumbnails("/mnt/video/tv/Show/Show - 01x09.mkv", 32, 18, 3)
	s.Thumbnails("/mnt/video/music/song.mkv", 32, 18, 3)

	inv := take(t, s, "tv")
	if len(inv.Libraries) != 1 || len(inv.Videos) != 1 || len(inv.Shows) != 1 {
		t.Fatalf("took %d libraries, %d videos and %d shows, want the TV library's one of each", len(inv.Libraries), len(inv.Videos), len(inv.Shows))
	}
	if v := inv.Videos[0]; v.Jellyfin == nil || len(v.Jellyfin.Thumbnails) != 1 {
		t.Errorf("the episode's thumbnails were not matched: %+v", v.Jellyfin)
	}
	if len(inv.JellyfinOnly) != 1 || !strings.HasSuffix(inv.JellyfinOnly[0].Path, "01x09.mkv") {
		t.Errorf("Jellyfin's videos Emby does not have are %+v, want the one in TV's folder", inv.JellyfinOnly)
	}
}

// A BIF none of whose names could be a video's beside it is a stray, as is a half-written one a stopped run left an hour or more ago. A BIF beside
// a video of a kind Emby plays, a disc's folder, or a video Emby has and found, is not, though Emby may not have the video yet.
func TestStrays(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Library("TV", "tvshows", "tv")
	// a video of a kind ebb does not know is still beside its BIF, as Emby has it and it was found
	s.BIF(s.Episode("tv/Show/Show - 01x01.rare"), "Show - 01x01-320-10.bif", 16, 9, 2)

	// the video was renamed since Emby looked, and its BIF was left with the old name
	renamed := s.Episode("tv/Show/Show - 01x02 - Old Title.mkv")
	dir := filepath.Dir(s.Local(renamed))
	if err := os.Rename(s.Local(renamed), filepath.Join(dir, "Show - 01x02 - New Title.mkv")); err != nil {
		t.Fatal(err)
	}
	old := s.BIF(renamed, "Show - 01x02 - Old Title-320-10.bif", 16, 9, 2)

	// a video Emby does not have yet keeps its BIF, and a disc's folder keeps its own
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}
	write("Show - 01x03.mp4")
	s.BIF(renamed, "Show - 01x03-320-10.bif", 16, 9, 2)
	if err := os.Mkdir(filepath.Join(dir, "Show - Special"), 0o750); err != nil {
		t.Fatal(err)
	}
	s.BIF(renamed, "Show - Special.bif", 16, 9, 2)

	// one beside nothing but the video's other files
	write("Show - 01x04.nfo")
	nfo := s.BIF(renamed, "Show - 01x04-320-10.bif", 16, 9, 2)

	// a half-written one from a stopped run, and one being written now
	stopped := write(".Show - 01x01-320-10.bif.123.ebb-tmp")
	long := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stopped, long, long); err != nil {
		t.Fatal(err)
	}
	write(".Show - 01x03-320-10.bif.456.ebb-tmp")

	inv := take(t, s)
	got := map[string]string{}
	for _, st := range inv.Strays {
		got[st.Path] = st.Why
		if st.Bytes == 0 || st.Changed.IsZero() {
			t.Errorf("stray %s has no size or time", st.Path)
		}
	}
	want := map[string]string{old: "no video of its name beside it", nfo: "no video of its name beside it", stopped: "half written, by a run of ebb that was stopped"}
	if len(got) != len(want) {
		t.Errorf("strays %v, want %v", got, want)
	}
	for path, why := range want {
		if got[path] != why {
			t.Errorf("%s is a stray for %q, want %q", path, got[path], why)
		}
	}
}

// The names of the videos a BIF could be beside: the front of a sized name, then the whole name.
func TestStems(t *testing.T) {
	t.Parallel()

	if got := inventory.Stems("Show - 01x01-320-10"); len(got) != 2 || got[0] != "Show - 01x01" || got[1] != "Show - 01x01-320-10" {
		t.Errorf("stems %q", got)
	}
	if got := inventory.Stems("Show - 01x01"); len(got) != 1 {
		t.Errorf("stems %q", got)
	}
	if got := inventory.Stem("/a/b/Show - 01x01.part1.mkv"); got != "Show - 01x01.part1" {
		t.Errorf("stem %q", got)
	}
}

// A video whose name has its accents kept apart, which a Mac's share lists but will not open, is marked as out of reach, its BIF with it, rather than
// its BIF taken for broken. A video with an ordinary name is not tried, and one that opens is not out of reach.
func TestUnreachableNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("not really a video"), 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}
	apart := norm.NFD.String("Cr\u00e8me Br\u00fbl\u00e9e")
	write(apart + "-320-10.bif")
	write("Plain-320-10.bif")
	videos := []*inventory.Video{{Local: write(apart + ".mp4")}, {Local: write("Plain.mp4")}}

	var tried []string
	refuses := func(path string) (*os.File, error) {
		tried = append(tried, path)

		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	inventory.FolderWith(t.Context(), dir, videos, refuses)

	far, plain := videos[0], videos[1]
	if !far.Found || !far.Unreachable || len(far.BIFs) != 1 || !errors.Is(far.BIFs[0].Err, inventory.ErrUnreachable) {
		t.Errorf("the video named with its accents apart is %+v, want it found, out of reach, and its BIF with it", far)
	}
	if !plain.Found || plain.Unreachable || len(plain.BIFs) != 1 || errors.Is(plain.BIFs[0].Err, inventory.ErrUnreachable) {
		t.Errorf("the plainly named video is %+v, want it found and in reach", plain)
	}
	if len(tried) != 1 || tried[0] != far.Local {
		t.Errorf("tried opening %q, want only the video named with its accents apart", tried)
	}

	if inventory.UnreachableWith(far.Local, os.Open) {
		t.Error("a video that opens is taken to be out of reach")
	}
}
