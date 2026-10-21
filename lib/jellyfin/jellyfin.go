// Package jellyfin reads what Jellyfin knows of its videos and their thumbnails, from Jellyfin's own database, without ever writing to it.
package jellyfin

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/readonly"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
)

// Video is one of Jellyfin's videos and the sets of thumbnails it has made of it, widest first.
type Video struct {
	// ID is Jellyfin's item ID, upper case with dashes, as its database spells it.
	ID string
	// Path is the video's file as Jellyfin sees it, and Type what Jellyfin takes it for: Episode, Movie, Trailer, Video.
	Path string
	Type string
	// Thumbnails is every set Jellyfin has made, the widest first. It is empty for a video Jellyfin has not got to.
	Thumbnails []trickplay.Info
}

// Widest is the video's widest set of thumbnails, and whether it has any.
func (v Video) Widest() (trickplay.Info, bool) {
	if len(v.Thumbnails) == 0 {
		return trickplay.Info{}, false
	}

	return v.Thumbnails[0], true
}

// DB is Jellyfin's database, open read-only.
type DB struct {
	db *sql.DB
}

// Open opens Jellyfin's database, jellyfin.db in its data folder, without being able to write to it.
func Open(ctx context.Context, path string) (*DB, error) {
	db, err := readonly.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}

// Close lets the database go.
func (d *DB) Close() error {
	return d.db.Close()
}

// selectThumbnails asks for each set of thumbnails and the video it is of. Jellyfin keys a set on the video and its width, so a video can have more
// than one.
const selectThumbnails = `SELECT t.ItemId, b.Path, b.Type, t.Width, t.Height, t.TileWidth, t.TileHeight, t.ThumbnailCount, t.Interval
FROM TrickplayInfos t JOIN BaseItems b ON b.Id = t.ItemId`

// Thumbnails is every video Jellyfin has made thumbnails of, keyed on the video's path.
func (d *DB) Thumbnails(ctx context.Context) (map[string]Video, error) {
	var got sets
	if err := readonly.Rows(ctx, d.db, got.start, got.each, selectThumbnails+" WHERE b.Path IS NOT NULL"); err != nil {
		return nil, err
	}

	videos := got.done()
	byPath := make(map[string]Video, len(videos))
	for _, v := range videos {
		byPath[v.Path] = v
	}

	return byPath, nil
}

// ByID is the video with this item ID and its thumbnails, if Jellyfin has it.
func (d *DB) ByID(ctx context.Context, id string) (Video, bool, error) {
	return d.one(ctx, "b.Id = ?", strings.ToUpper(id))
}

// ByPath is the video at this path, as Jellyfin sees it, and its thumbnails, if Jellyfin has it.
func (d *DB) ByPath(ctx context.Context, path string) (Video, bool, error) {
	return d.one(ctx, "b.Path = ?", path)
}

// one is the video the condition picks, with its thumbnails or without: a video Jellyfin has not made any of yet is still one it has.
func (d *DB) one(ctx context.Context, where, arg string) (Video, bool, error) {
	v := Video{}
	err := readonly.Retry(ctx, func() error {
		return d.db.QueryRowContext(ctx, "SELECT b.Id, b.Path, b.Type FROM BaseItems b WHERE "+where, arg).Scan(&v.ID, &v.Path, &v.Type)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Video{}, false, nil
	}
	if err != nil {
		return Video{}, false, err
	}
	v.Type = shortType(v.Type)

	var got sets
	if err := readonly.Rows(ctx, d.db, got.start, got.each, selectThumbnails+" WHERE t.ItemId = ?", v.ID); err != nil {
		return Video{}, false, err
	}
	if found := got.done(); len(found) == 1 {
		v.Thumbnails = found[0].Thumbnails
	}

	return v, true, nil
}

// sets gathers rows of selectThumbnails into videos, each with every set it has.
type sets struct {
	videos []Video
	at     map[string]int
}

// start forgets what an earlier try gathered.
func (s *sets) start() {
	s.videos, s.at = nil, map[string]int{}
}

// each gathers one row.
func (s *sets) each(rows *sql.Rows) error {
	var (
		path, kind sql.NullString
		i          trickplay.Info
		interval   int64
	)
	if err := rows.Scan(&i.ItemID, &path, &kind, &i.Width, &i.Height, &i.Columns, &i.Rows, &i.Count, &interval); err != nil {
		return err
	}
	i.Interval = time.Duration(interval) * time.Millisecond

	n, ok := s.at[i.ItemID]
	if !ok {
		n = len(s.videos)
		s.at[i.ItemID] = n
		s.videos = append(s.videos, Video{ID: i.ItemID, Path: path.String, Type: shortType(kind.String)})
	}
	s.videos[n].Thumbnails = append(s.videos[n].Thumbnails, i)

	return nil
}

// done is the videos gathered, each with its sets widest first.
func (s *sets) done() []Video {
	for _, v := range s.videos {
		slices.SortFunc(v.Thumbnails, func(a, b trickplay.Info) int { return b.Width - a.Width })
	}

	return s.videos
}

// shortType is Jellyfin's type without the namespace it is written with: Episode for MediaBrowser.Controller.Entities.TV.Episode.
func shortType(t string) string {
	return t[strings.LastIndex(t, ".")+1:]
}
