package emby_test

import (
	"testing"

	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/servertest"
)

// open lays out two libraries, one inside the other's folder, and opens Emby's database.
func open(t *testing.T) *emby.DB {
	t.Helper()

	s := servertest.New(t)
	s.Library("Movies", "movies", "movies")
	s.Library("Anime (Movies)", "movies", "movies/anime")
	s.Library("Music", "music", "music")
	s.Show("tv/Show (2020)")
	s.Film("movies/Film (2000)/Film (2000).mkv")
	s.Film("movies/anime/Akira (1988)/Akira (1988).mkv")
	s.Trailer("movies/Film (2000)/trailers/Film (2000)-trailer.mkv")
	s.Episode("elsewhere/Show - 01x01.mkv")
	s.Done()

	db, err := emby.Open(t.Context(), s.EmbyDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// Each library comes with its folders and the settings that bear on thumbnails, in order of name.
func TestTheLibraries(t *testing.T) {
	t.Parallel()

	db := open(t)
	libs, err := db.Libraries(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(libs) != 3 || libs[0].Name != "Anime (Movies)" || libs[1].Name != "Movies" || libs[2].Name != "Music" {
		t.Fatalf("libraries %+v, want Anime (Movies), Movies and Music", libs)
	}
	m := libs[1]
	if len(m.Folders) != 1 || m.Folders[0] != "/mnt/video/movies" || m.ContentType != "movies" || !m.BesideVideos || !m.Thumbnails || m.Interval.Seconds() != 10 {
		t.Errorf("Movies is %+v", m)
	}
}

// Each item comes with its kind and the library whose folder holds it, the one deepest in when folders nest; one in no library's folder has none.
func TestTheItems(t *testing.T) {
	t.Parallel()

	db := open(t)
	libs, err := db.Libraries(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	items, err := db.Items(t.Context(), libs)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]struct {
		kind emby.Kind
		lib  string
	}{
		"/mnt/video/tv/Show (2020)":                                      {emby.Show, ""},
		"/mnt/video/movies/Film (2000)/Film (2000).mkv":                  {emby.Film, "Movies"},
		"/mnt/video/movies/anime/Akira (1988)/Akira (1988).mkv":          {emby.Film, "Anime (Movies)"},
		"/mnt/video/movies/Film (2000)/trailers/Film (2000)-trailer.mkv": {emby.Extra, "Movies"},
		"/mnt/video/elsewhere/Show - 01x01.mkv":                          {emby.Episode, ""},
	}
	if len(items) != len(want) {
		t.Errorf("%d items, want %d: %+v", len(items), len(want), items)
	}
	for _, it := range items {
		w, ok := want[it.Path]
		if !ok || it.Kind != w.kind || it.Library != w.lib || it.ID == 0 {
			t.Errorf("%s is a %s in %q (%d), want a %s in %q", it.Path, it.Kind, it.Library, it.ID, w.kind, w.lib)
		}
	}
}

// An item is found by its exact path, case and all, though Emby's index of paths ignores case.
func TestFindingAnItem(t *testing.T) {
	t.Parallel()

	db := open(t)
	libs, err := db.Libraries(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	it, found, err := db.ByPath(t.Context(), libs, "/mnt/video/movies/Film (2000)/Film (2000).mkv")
	if err != nil || !found || it.Kind != emby.Film || it.Library != "Movies" {
		t.Errorf("found %+v (%t, %v), want the film in Movies", it, found, err)
	}
	if _, found, err := db.ByPath(t.Context(), libs, "/mnt/video/movies/film (2000)/film (2000).mkv"); found || err != nil {
		t.Errorf("a path in the wrong case found an item (%v)", err)
	}

	w, h, err := db.Size(t.Context(), it.ID)
	if err != nil || w != 1920 || h != 1080 {
		t.Errorf("the film is %dx%d (%v), want 1920x1080", w, h, err)
	}
}

// A path is within a folder when it is the folder or anything in it, and not merely a name that starts the same.
func TestWithin(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		path, folder string
		want         bool
	}{
		{"/mnt/video/tv/x.mkv", "/mnt/video/tv", true},
		{"/mnt/video/tv/x.mkv", "/mnt/video/tv/", true},
		{"/mnt/video/tv", "/mnt/video/tv", true},
		{"/mnt/video/tv2/x.mkv", "/mnt/video/tv", false},
	} {
		if got := emby.Within(c.path, c.folder); got != c.want {
			t.Errorf("%s within %s = %t, want %t", c.path, c.folder, got, c.want)
		}
	}
	if emby.Kind(0).String() != "unknown" || emby.Episode.String() != "episode" {
		t.Error("the kinds are not said as a person says them")
	}
}
