// Package servertest lays out a made-up Emby and Jellyfin for tests: their databases, holding only the tables and columns ebb reads, a media
// folder with videos in it, Jellyfin's thumbnail sheets, and BIFs beside the videos.
//
// The servers see the videos under /mnt/video, as the real ones do, and the test finds them under Media, so every test also crosses from one to
// the other as ebb does when it runs on another machine.
package servertest

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"

	// the SQLite driver, to write the made-up databases
	_ "github.com/ncruces/go-sqlite3/driver"
)

// ServerRoot is where the made-up servers see the videos.
const ServerRoot = "/mnt/video"

// Servers is a made-up Emby and Jellyfin.
type Servers struct {
	t testing.TB
	// Media is where the test finds what the servers see under ServerRoot, and Trickplay is Jellyfin's trickplay folder.
	Media     string
	Trickplay string
	// EmbyDB and JellyfinDB are the two databases.
	EmbyDB     string
	JellyfinDB string

	emby, jellyfin *sql.DB
	nextID         int64
}

// Emby's own numbers for the kinds of item, as its database has them.
const (
	typeLibrary = 4
	typeFilm    = 5
	typeShow    = 6
	typeEpisode = 8
	typeTrailer = 10
)

const embySchema = `
CREATE TABLE MediaItems (Id INTEGER PRIMARY KEY AUTOINCREMENT, type INT NOT NULL, Name TEXT, Path TEXT NULL, Width INT, Height INT);
CREATE INDEX idx_MediaItems35 ON MediaItems(Path COLLATE NOCASE) WHERE Path NOT NULL;
CREATE INDEX idx_MediaItems33b ON MediaItems(type);
CREATE TABLE ItemExtradataTypes (ExtradataTypeId INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT);
CREATE TABLE ItemExtradata (ItemId INT NOT NULL, ExtradataTypeId INT NOT NULL, Value TEXT NOT NULL);
CREATE UNIQUE INDEX ItemExtradataUnique1 ON ItemExtradata (ItemId, ExtradataTypeId);
INSERT INTO ItemExtradataTypes (Name) VALUES ('ThumbnailImageExtraction'), ('IntroDetectionExtradata'), ('LibraryOptions'), ('IntroFingerprint');
`

const jellyfinSchema = `
CREATE TABLE BaseItems (Id TEXT NOT NULL PRIMARY KEY, Path TEXT NULL, Type TEXT NOT NULL);
CREATE INDEX IX_BaseItems_Path ON BaseItems (Path);
CREATE TABLE TrickplayInfos (ItemId TEXT NOT NULL, Width INTEGER NOT NULL, Height INTEGER NOT NULL, TileWidth INTEGER NOT NULL, TileHeight INTEGER NOT NULL, ThumbnailCount INTEGER NOT NULL, Interval INTEGER NOT NULL, Bandwidth INTEGER NOT NULL, PRIMARY KEY (ItemId, Width));
`

// New is an Emby and a Jellyfin with no libraries yet. Done must be called once they are laid out, before ebb reads them.
func New(tb testing.TB) *Servers {
	tb.Helper()

	root := tb.TempDir()
	s := &Servers{t: tb, Media: filepath.Join(root, "media"), Trickplay: filepath.Join(root, "trickplay"), EmbyDB: filepath.Join(root, "emby", "library.db"), JellyfinDB: filepath.Join(root, "jellyfin", "jellyfin.db"), nextID: 1000}
	for _, dir := range []string{s.Media, s.Trickplay, filepath.Dir(s.EmbyDB), filepath.Dir(s.JellyfinDB)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	s.emby = s.open(s.EmbyDB, embySchema)
	s.jellyfin = s.open(s.JellyfinDB, jellyfinSchema)
	tb.Cleanup(func() { s.Done() })

	return s
}

// open opens a database to write to, making it with this schema first when the schema is given.
func (s *Servers) open(path, schema string) *sql.DB {
	s.t.Helper()

	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		s.t.Fatal(err)
	}
	if schema != "" {
		if _, err := db.ExecContext(s.t.Context(), schema); err != nil {
			s.t.Fatal(err)
		}
	}

	return db
}

// embyDB and jellyfinDB are the databases to write to, opened again if Done closed them.
func (s *Servers) embyDB() *sql.DB {
	if s.emby == nil {
		s.emby = s.open(s.EmbyDB, "")
	}

	return s.emby
}

func (s *Servers) jellyfinDB() *sql.DB {
	if s.jellyfin == nil {
		s.jellyfin = s.open(s.JellyfinDB, "")
	}

	return s.jellyfin
}

// Done closes the databases, so all that was written is in their files for ebb to read. More can be laid out after, and Done called again.
func (s *Servers) Done() {
	for _, db := range []*sql.DB{s.emby, s.jellyfin} {
		if db != nil {
			_ = db.Close()
		}
	}
	s.emby, s.jellyfin = nil, nil
}

// MediaPaths is the --media-paths setting that says where the test finds what the servers see.
func (s *Servers) MediaPaths() string {
	return ServerRoot + "=" + s.Media
}

// Local is where the test finds what the servers call path.
func (s *Servers) Local(path string) string {
	return filepath.Join(s.Media, filepath.FromSlash(strings.TrimPrefix(path, ServerRoot)))
}

// exec runs a statement on a database, failing the test if it fails.
func (s *Servers) exec(db *sql.DB, query string, args ...any) {
	s.t.Helper()

	if _, err := db.ExecContext(s.t.Context(), query, args...); err != nil {
		s.t.Fatalf("%s: %v", query, err)
	}
}

// Library adds a library of this name over folders under ServerRoot, as Emby keeps one: an item of its own, with its settings hung off it.
func (s *Servers) Library(name, contentType string, folders ...string) {
	s.t.Helper()

	s.nextID++
	id := s.nextID
	s.exec(s.embyDB(), "INSERT INTO MediaItems (Id, type, Name) VALUES (?, ?, ?)", id, typeLibrary, name)

	infos := make([]map[string]string, 0, len(folders))
	for _, f := range folders {
		infos = append(infos, map[string]string{"Path": ServerRoot + "/" + f})
	}
	raw, err := json.Marshal(map[string]any{"ContentType": contentType, "PathInfos": infos, "SaveLocalThumbnailSets": true, "EnableChapterImageExtraction": true, "ThumbnailImagesIntervalSeconds": 10})
	if err != nil {
		s.t.Fatal(err)
	}
	s.exec(s.embyDB(), "INSERT INTO ItemExtradata (ItemId, ExtradataTypeId, Value) VALUES (?, (SELECT ExtradataTypeId FROM ItemExtradataTypes WHERE Name = 'LibraryOptions'), ?)", id, string(raw))
}

// Film adds a film at path under ServerRoot, with a video file there. It returns the path as the servers see it.
func (s *Servers) Film(path string) string {
	s.t.Helper()

	return s.video(typeFilm, path)
}

// Episode adds an episode at path under ServerRoot, with a video file there. It returns the path as the servers see it.
func (s *Servers) Episode(path string) string {
	s.t.Helper()

	return s.video(typeEpisode, path)
}

// Trailer adds a trailer, an extra, at path under ServerRoot, with a video file there. It returns the path as the servers see it.
func (s *Servers) Trailer(path string) string {
	s.t.Helper()

	return s.video(typeTrailer, path)
}

// Show adds a show whose folder is path under ServerRoot.
func (s *Servers) Show(path string) {
	s.t.Helper()

	s.nextID++
	s.exec(s.embyDB(), "INSERT INTO MediaItems (Id, type, Name, Path) VALUES (?, ?, ?, ?)", s.nextID, typeShow, filepath.Base(path), ServerRoot+"/"+path)
}

// video adds an item of this kind and writes a stand-in for its file.
func (s *Servers) video(kind int, path string) string {
	s.t.Helper()

	server := ServerRoot + "/" + path
	s.nextID++
	s.exec(s.embyDB(), "INSERT INTO MediaItems (Id, type, Name, Path, Width, Height) VALUES (?, ?, ?, ?, 1920, 1080)", s.nextID, kind, filepath.Base(path), server)

	local := s.Local(server)
	if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("not really a video"), 0o600); err != nil {
		s.t.Fatal(err)
	}

	return server
}

// Thumbnails gives the video at path, as the servers see it, a set of Jellyfin thumbnails of this size, count thumbnails ten seconds apart on
// sheets of ten by ten, and lays out the sheets. It returns the set as Jellyfin's database has it.
func (s *Servers) Thumbnails(path string, width, height, count int) trickplay.Info {
	s.t.Helper()

	// a video Jellyfin already has keeps its ID
	var id string
	err := s.jellyfinDB().QueryRowContext(s.t.Context(), "SELECT Id FROM BaseItems WHERE Path = ?", path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = s.JellyfinVideo(path)
	} else if err != nil {
		s.t.Fatal(err)
	}

	i := trickplay.Info{ItemID: id, Width: width, Height: height, Columns: 10, Rows: 10, Count: count, Interval: 10 * time.Second}
	s.exec(s.jellyfinDB(), "INSERT INTO TrickplayInfos VALUES (?, ?, ?, ?, ?, ?, ?, 0)", i.ItemID, i.Width, i.Height, i.Columns, i.Rows, i.Count, i.Interval.Milliseconds())
	s.Sheets(i)

	return i
}

// JellyfinVideo adds a video Jellyfin has and has made no thumbnails of yet, and returns its ID.
func (s *Servers) JellyfinVideo(path string) string {
	s.t.Helper()

	s.nextID++
	id := fmt.Sprintf("0B%06d-0000-0000-0000-%012d", s.nextID, s.nextID)
	s.exec(s.jellyfinDB(), "INSERT INTO BaseItems (Id, Path, Type) VALUES (?, ?, ?)", id, path, "MediaBrowser.Controller.Entities.TV.Episode")

	return id
}

// Shade is the grey Jellyfin's thumbnail k is painted, so a test can tell which thumbnail a BIF's picture was cut from.
func Shade(k int) uint8 {
	return uint8(16 + 24*(k%10)) //nolint:gosec // under 256 for every k
}

// Sheets writes the sheets for a set of thumbnails where Jellyfin keeps them, each thumbnail painted its own shade. Every sheet is full size, as
// Jellyfin's are, the last blank after its last thumbnail.
func (s *Servers) Sheets(i trickplay.Info) {
	s.t.Helper()

	dir := i.Folder(s.Trickplay)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.t.Fatal(err)
	}

	per := i.Columns * i.Rows
	for n := range i.Sheets() {
		sheet := image.NewGray(image.Rect(0, 0, i.Columns*i.Width, i.Rows*i.Height))
		for k := range min(per, i.Count-n*per) {
			x, y := (k%i.Columns)*i.Width, (k/i.Columns)*i.Height
			for yy := y; yy < y+i.Height; yy++ {
				for xx := x; xx < x+i.Width; xx++ {
					sheet.SetGray(xx, yy, color.Gray{Y: Shade(n*per + k)})
				}
			}
		}

		var b bytes.Buffer
		if err := jpeg.Encode(&b, sheet, &jpeg.Options{Quality: 95}); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(n)+".jpg"), b.Bytes(), 0o600); err != nil {
			s.t.Fatal(err)
		}
	}
}

// BIF writes a BIF named name beside the video at path, as the servers see it, of frames pictures this size ten seconds apart, as Emby would make
// one. It returns where the test finds it.
func (s *Servers) BIF(path, name string, width, height, frames int) string {
	s.t.Helper()

	img := image.NewGray(image.Rect(0, 0, width, height))
	var pic bytes.Buffer
	if err := jpeg.Encode(&pic, img, nil); err != nil {
		s.t.Fatal(err)
	}

	pics := make([][]byte, frames)
	for k := range pics {
		pics[k] = pic.Bytes()
	}

	var b bytes.Buffer
	if err := bif.Write(&b, 10*time.Second, pics); err != nil {
		s.t.Fatal(err)
	}

	local := filepath.Join(filepath.Dir(s.Local(path)), name)
	if err := os.WriteFile(local, b.Bytes(), 0o600); err != nil {
		s.t.Fatal(err)
	}

	return local
}

// Read is what the BIF the test finds at path holds, failing the test if it cannot be read.
func Read(tb testing.TB, path string) bif.Info {
	tb.Helper()

	f, err := os.Open(path) //nolint:gosec // a test's own file
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		tb.Fatal(err)
	}
	info, err := bif.Read(f, fi.Size())
	if err != nil {
		tb.Fatalf("%s: %v", path, err)
	}

	return info
}

// Frames is the pictures of the BIF the test finds at path, in order, read straight from its index.
func Frames(tb testing.TB, path string) [][]byte {
	tb.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // a test's own file
	if err != nil {
		tb.Fatal(err)
	}

	n := int(binary.LittleEndian.Uint32(raw[12:]))
	frames := make([][]byte, n)
	for k := range n {
		from, to := binary.LittleEndian.Uint32(raw[68+8*k:]), binary.LittleEndian.Uint32(raw[76+8*k:])
		frames[k] = raw[from:to]
	}

	return frames
}

// ShadeOf is the grey a picture is painted, read at its middle.
func ShadeOf(tb testing.TB, pic []byte) uint8 {
	tb.Helper()

	img, err := jpeg.Decode(bytes.NewReader(pic))
	if err != nil {
		tb.Fatal(err)
	}
	r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()

	return uint8((r + g + b) / 3 >> 8) //nolint:gosec // a 16-bit colour's top byte
}
