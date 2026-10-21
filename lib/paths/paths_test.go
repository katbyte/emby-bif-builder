package paths_test

import (
	"testing"

	"github.com/katbyte/emby-bif-builder/lib/paths"
)

// kts is the mapping for a Mac that sees the NAS's video share, where two of the servers' folders sit deeper in than the rest.
const kts = "/mnt/video/documentaries=/Volumes/video/docu/documentary, /mnt/video/snow=/Volumes/video/sport/snow/, /mnt/video=/Volumes/video"

// A path is found under the deepest server folder that holds it, and one in none of them is where the servers see it.
func TestWhereAPathIsFound(t *testing.T) {
	t.Parallel()

	m, err := paths.Parse(kts)
	if err != nil {
		t.Fatal(err)
	}

	for server, local := range map[string]string{
		"/mnt/video/tv/Show/Show - 01x01.mkv":         "/Volumes/video/tv/Show/Show - 01x01.mkv",
		"/mnt/video/documentaries/Doc (2001)/Doc.mkv": "/Volumes/video/docu/documentary/Doc (2001)/Doc.mkv",
		"/mnt/video/snow/Run.mkv":                     "/Volumes/video/sport/snow/Run.mkv",
		"/mnt/video/snowboarding/Run.mkv":             "/Volumes/video/snowboarding/Run.mkv",
		"/mnt/music/song.mp3":                         "/mnt/music/song.mp3",
	} {
		if got := m.Local(server); got != local {
			t.Errorf("%s is found at %s, want %s", server, got, local)
		}
		if back := m.Server(local); back != server {
			t.Errorf("%s is %s to the servers, want %s", local, back, server)
		}
	}

	if m.String() != "/mnt/video/documentaries=/Volumes/video/docu/documentary,/mnt/video/snow=/Volumes/video/sport/snow,/mnt/video=/Volumes/video" {
		t.Errorf("the mapping reads back as %s", m)
	}
}

// No mapping leaves every path as it is, and a mapping written wrongly says how it should be written.
func TestMappingsWrittenWrongly(t *testing.T) {
	t.Parallel()

	m, err := paths.Parse(" , ")
	if err != nil || len(m) != 0 || m.Local("/x/y") != "/x/y" {
		t.Errorf("no mapping gave %v (%v)", m, err)
	}

	for _, bad := range []string{"/mnt/video", "=/Volumes/video", "/mnt/video=", "/mnt/video=/a,/mnt/video/=/b", "/=/Volumes/video"} {
		if _, err := paths.Parse(bad); err == nil {
			t.Errorf("%q was taken as a mapping", bad)
		}
	}
}
