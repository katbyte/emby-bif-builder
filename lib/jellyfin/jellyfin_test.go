package jellyfin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/servertest"
)

// Every video Jellyfin has made thumbnails of is found by its path, with each set it has made, the widest first, and a video it has not got to
// yet is not among them.
func TestEveryVideosThumbnails(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	two := s.Episode("tv/Show/Show - 01x01.mkv")
	small := s.Thumbnails(two, 32, 18, 5)
	big := s.Thumbnails(two, 64, 36, 5)
	s.JellyfinVideo("/mnt/video/tv/Show/Show - 01x02.mkv")
	s.Done()

	db, err := jellyfin.Open(t.Context(), s.JellyfinDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	all, err := db.Thumbnails(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("%d videos have thumbnails, want 1: %+v", len(all), all)
	}

	v := all[two]
	if v.ID != big.ItemID || v.Type != "Episode" || len(v.Thumbnails) != 2 || v.Thumbnails[0] != big || v.Thumbnails[1] != small {
		t.Errorf("the video is %+v, want both sets, the 64-wide first", v)
	}
	if w, ok := v.Widest(); !ok || w.Width != 64 || w.Interval != 10*time.Second {
		t.Errorf("the widest set is %+v", w)
	}
}

// A video is found by its item ID in either case, or by its path, with or without thumbnails, and one Jellyfin does not have is not found.
func TestFindingAVideo(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	with := s.Episode("tv/Show/Show - 01x01.mkv")
	set := s.Thumbnails(with, 32, 18, 5)
	id := s.JellyfinVideo("/mnt/video/tv/Show/Show - 01x02.mkv")
	s.Done()

	db, err := jellyfin.Open(t.Context(), s.JellyfinDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	v, found, err := db.ByID(t.Context(), strings.ToLower(set.ItemID))
	if err != nil || !found || v.Path != with || len(v.Thumbnails) != 1 {
		t.Errorf("by ID found %+v (%t, %v)", v, found, err)
	}

	v, found, err = db.ByPath(t.Context(), "/mnt/video/tv/Show/Show - 01x02.mkv")
	if err != nil || !found || v.ID != id || len(v.Thumbnails) != 0 {
		t.Errorf("by path found %+v (%t, %v), want the video with no thumbnails", v, found, err)
	}
	if _, ok := v.Widest(); ok {
		t.Error("a video with no thumbnails has a widest set")
	}

	if _, found, err := db.ByPath(t.Context(), "/mnt/video/nothing.mkv"); found || err != nil {
		t.Errorf("a video Jellyfin does not have was found (%v)", err)
	}
}

// A database that is not there, or a folder in its place, is said.
func TestADatabaseThatIsNotThere(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Done()

	if _, err := jellyfin.Open(t.Context(), s.JellyfinDB+".missing"); err == nil {
		t.Error("a database that is not there was opened")
	}
	if _, err := jellyfin.Open(t.Context(), s.Trickplay); err == nil || !strings.Contains(err.Error(), "is a folder") {
		t.Errorf("a folder opened as a database gave %v", err)
	}
}
