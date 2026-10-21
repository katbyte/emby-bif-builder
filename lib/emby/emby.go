// Package emby reads Emby's libraries and the videos in them, from Emby's own database, without ever writing to it.
package emby

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/readonly"
)

// Kind is what an item is to Emby.
type Kind int

const (
	Film Kind = iota + 1
	Show
	Episode
	// Extra is a trailer, theme video, behind the scenes and the like: a video that belongs to a film or a show.
	Extra
)

// String is the kind as a person says it.
func (k Kind) String() string {
	switch k {
	case Film:
		return "film"
	case Show:
		return "show"
	case Episode:
		return "episode"
	case Extra:
		return "extra"
	default:
		return "unknown"
	}
}

// kinds is Emby's own numbers for the kinds of item ebb reads. Emby does not write them down anywhere: they were read off Emby 4.11's database,
// where 5 holds every film, 6 every show, 8 every episode, 10 the trailers and 18 the other extras.
var kinds = map[int]Kind{5: Film, 6: Show, 8: Episode, 10: Extra, 18: Extra}

// libraryType is Emby's number for a library's own item, which its settings hang off, read off the same database.
const libraryType = 4

// Item is a film, show, episode or extra Emby has. A show's path is its folder; the rest are videos, and their path the video's file.
type Item struct {
	ID   int64
	Kind Kind
	// Path is where Emby sees it.
	Path string
	// Library is the name of the library whose folders hold it, "" for an item in none.
	Library string
}

// Library is one of Emby's libraries and the settings of it that bear on thumbnails.
type Library struct {
	Name string
	// Folders are where its videos are, as Emby sees them.
	Folders []string
	// ContentType is what Emby takes it to hold: movies, tvshows, music.
	ContentType string
	// BesideVideos is Emby's "save thumbnails with media": a BIF beside the video is where Emby both looks for and writes one.
	BesideVideos bool
	// Thumbnails is whether Emby makes thumbnails in it at all, and Interval how far apart it takes them.
	Thumbnails bool
	Interval   time.Duration
}

// DB is Emby's database, open read-only.
type DB struct {
	db *sql.DB
}

// Open opens Emby's database, library.db in its data folder, without being able to write to it.
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

// libraryOptions is the part of each library's settings that ebb reads. Emby keeps them as JSON beside the library's own item.
type libraryOptions struct {
	ContentType                    string
	PathInfos                      []struct{ Path string }
	SaveLocalThumbnailSets         bool
	EnableChapterImageExtraction   bool
	ThumbnailImagesIntervalSeconds int
}

// Libraries is every library that has a folder, by name.
func (d *DB) Libraries(ctx context.Context) ([]Library, error) {
	var libs []Library
	each := func(rows *sql.Rows) error {
		var (
			name sql.NullString
			raw  string
		)
		if err := rows.Scan(&name, &raw); err != nil {
			return err
		}

		var o libraryOptions
		if err := json.Unmarshal([]byte(raw), &o); err != nil {
			return fmt.Errorf("reading the settings of Emby's library %s: %w", name.String, err)
		}

		lib := Library{Name: name.String, ContentType: o.ContentType, BesideVideos: o.SaveLocalThumbnailSets, Thumbnails: o.EnableChapterImageExtraction, Interval: time.Duration(o.ThumbnailImagesIntervalSeconds) * time.Second}
		for _, p := range o.PathInfos {
			// playlists and collections live in Emby's own folders, named with a %
			if p.Path != "" && !strings.HasPrefix(p.Path, "%") {
				lib.Folders = append(lib.Folders, p.Path)
			}
		}
		if len(lib.Folders) > 0 {
			libs = append(libs, lib)
		}

		return nil
	}

	// asked from the libraries' own items, of which there are a dozen, since the settings sit among every video's intro fingerprints: hundreds of
	// megabytes to read through over a share
	if err := readonly.Rows(ctx, d.db, func() { libs = nil }, each, `SELECT m.Name, e.Value FROM MediaItems m
JOIN ItemExtradata e ON e.ItemId = m.Id AND e.ExtradataTypeId = (SELECT ExtradataTypeId FROM ItemExtradataTypes WHERE Name = 'LibraryOptions')
WHERE m.type = ?`, libraryType); err != nil {
		return nil, fmt.Errorf("reading Emby's libraries: %w", err)
	}

	slices.SortFunc(libs, func(a, b Library) int { return strings.Compare(a.Name, b.Name) })

	return libs, nil
}

// Items is every film, show, episode and extra that has a path, each with the library that holds it.
//
// The kinds and the paths are asked for apart, as each is read from an index of its own without touching the items themselves: a few seconds,
// where reading the items would be minutes over a network share.
func (d *DB) Items(ctx context.Context, libs []Library) ([]Item, error) {
	numbers := slices.Sorted(maps.Keys(kinds))
	args := make([]any, 0, len(numbers))
	for _, n := range numbers {
		args = append(args, n)
	}

	var kindOf map[int64]Kind
	kind := func(rows *sql.Rows) error {
		var (
			id int64
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		kindOf[id] = kinds[n]

		return nil
	}
	if err := readonly.Rows(ctx, d.db, func() { kindOf = map[int64]Kind{} }, kind, "SELECT Id, type FROM MediaItems WHERE type IN (?"+strings.Repeat(", ?", len(args)-1)+")", args...); err != nil {
		return nil, fmt.Errorf("reading Emby's items: %w", err)
	}

	var items []Item
	path := func(rows *sql.Rows) error {
		var it Item
		if err := rows.Scan(&it.ID, &it.Path); err != nil {
			return err
		}

		k, ok := kindOf[it.ID]
		if ok && !strings.HasPrefix(it.Path, "%") {
			it.Kind, it.Library = k, LibraryOf(libs, it.Path)
			items = append(items, it)
		}

		return nil
	}
	if err := readonly.Rows(ctx, d.db, func() { items = make([]Item, 0, len(kindOf)) }, path, "SELECT Id, Path FROM MediaItems WHERE Path IS NOT NULL"); err != nil {
		return nil, fmt.Errorf("reading Emby's items' paths: %w", err)
	}

	return items, nil
}

// ByPath is the item at this path, as Emby sees it, if Emby has one.
func (d *DB) ByPath(ctx context.Context, libs []Library, path string) (Item, bool, error) {
	var (
		found Item
		ok    bool
	)
	each := func(rows *sql.Rows) error {
		var (
			it Item
			n  int
		)
		if err := rows.Scan(&it.ID, &n, &it.Path); err != nil {
			return err
		}

		if kind, known := kinds[n]; known && it.Path == path && !ok {
			it.Kind, it.Library = kind, LibraryOf(libs, it.Path)
			found, ok = it, true
		}

		return nil
	}

	// Emby's index of paths ignores case, so it is asked that way and the answer held to the exact path
	err := readonly.Rows(ctx, d.db, func() { found, ok = Item{}, false }, each, "SELECT Id, type, Path FROM MediaItems WHERE Path = ? COLLATE NOCASE", path)

	return found, ok, err
}

// Size is the picture size Emby read from a video, 0 by 0 when it has none.
func (d *DB) Size(ctx context.Context, id int64) (width, height int, err error) {
	var w, h sql.NullInt64
	err = readonly.Retry(ctx, func() error {
		return d.db.QueryRowContext(ctx, "SELECT Width, Height FROM MediaItems WHERE Id = ?", id).Scan(&w, &h)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}

	return int(w.Int64), int(h.Int64), err
}

// LibraryOf is the name of the library whose folder holds the path, the one deepest in when folders nest, or "" for none.
func LibraryOf(libs []Library, path string) string {
	best, depth := "", -1
	for _, lib := range libs {
		for _, f := range lib.Folders {
			if Within(path, f) && len(f) > depth {
				best, depth = lib.Name, len(f)
			}
		}
	}

	return best
}

// Within says whether the path is the folder or anything in it.
func Within(path, folder string) bool {
	folder = strings.TrimRight(folder, "/")

	return path == folder || strings.HasPrefix(path, folder+"/")
}
