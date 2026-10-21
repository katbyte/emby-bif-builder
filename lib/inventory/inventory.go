// Package inventory puts together what each of Emby's videos has: where ebb finds it, the BIFs beside it, and the thumbnails Jellyfin has made of it.
package inventory

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/paths"
	"github.com/katbyte/go-kt/parallel"
	"golang.org/x/text/unicode/norm"
)

// Style is how a BIF beside a video is named.
type Style int

const (
	// Sized is <video>-<width>-<seconds>.bif, as Emby names its own.
	Sized Style = iota + 1
	// Bare is <video>.bif, as Roku players named them.
	Bare
)

// BIF is one BIF beside a video.
type BIF struct {
	// Path is where ebb finds it, and Name its file name.
	Path string
	Name string
	// Style is how it is named, and for a Sized one Named the width and seconds its name gives, as "320-10".
	Style Style
	Named string
	// Bytes and Changed are the file's size and when it was last written.
	Bytes   int64
	Changed time.Time
	// Info is what it holds, and Err why it could not be read when it could not.
	Info bif.Info
	Err  error
}

// Video is one of Emby's videos and what it has.
type Video struct {
	emby.Item
	// Local is where ebb finds the video, and Found whether it is there. Err is why its folder could not be read, when it could not.
	Local string
	Found bool
	Err   error
	// BIFs is every BIF beside it.
	BIFs []BIF
	// Jellyfin is the video as Jellyfin has it, when Jellyfin has made thumbnails of it.
	Jellyfin *jellyfin.Video
}

// Widest is the widest of the video's BIFs that can be read, and whether it has one.
func (v *Video) Widest() (BIF, bool) {
	var (
		best  BIF
		found bool
	)
	for _, b := range v.BIFs {
		if b.Err == nil && (!found || b.Info.Width > best.Info.Width) {
			best, found = b, true
		}
	}

	return best, found
}

// Stray is a file beside the videos that nothing needs: a BIF with no video of its name beside it, left when its video was renamed or removed, or
// a half-written BIF a run of ebb that was stopped left behind.
type Stray struct {
	Path    string
	Bytes   int64
	Changed time.Time
	// Why is what makes it a stray.
	Why string
}

// leftover is how long a half-written BIF must have been left before it is taken to be one a stopped run left, and not one a run is writing now.
const leftover = time.Hour

// videoExtensions is the kinds of video file Emby plays, by extension, for telling whether a BIF's video is still beside it.
var videoExtensions = map[string]bool{
	".3gp": true, ".asf": true, ".avi": true, ".divx": true, ".dvr-ms": true, ".f4v": true, ".flv": true, ".img": true, ".iso": true, ".m2t": true,
	".m2ts": true, ".m2v": true, ".m4v": true, ".mk3d": true, ".mkv": true, ".mov": true, ".mp4": true, ".mpeg": true, ".mpg": true, ".mts": true,
	".ogm": true, ".ogv": true, ".rec": true, ".rm": true, ".rmvb": true, ".strm": true, ".ts": true, ".vob": true, ".webm": true, ".wmv": true,
	".wtv": true, ".xvid": true,
}

// Options is what to take stock of, and from where.
type Options struct {
	Emby *emby.DB
	// Jellyfin is Jellyfin's database, nil to leave Jellyfin out.
	Jellyfin *jellyfin.DB
	// Paths is where ebb finds what the servers see.
	Paths paths.Map
	// Libraries is the names of the only libraries to look at, all of them when empty.
	Libraries []string
	// Workers is how many folders are read at once.
	Workers int
	// Progress is told how many of the folders have been read, after each one.
	Progress func(done, total int)
}

// Inventory is what Emby's videos have.
type Inventory struct {
	// Libraries is the libraries looked at.
	Libraries []emby.Library
	// Shows is the shows in them, for counting: a show has no file of its own.
	Shows []emby.Item
	// Videos is every film, episode and extra in them.
	Videos []*Video
	// JellyfinOnly is the videos in those libraries' folders that Jellyfin has made thumbnails of and Emby does not have.
	JellyfinOnly []jellyfin.Video
	// Strays is the files beside the videos that nothing needs, in the folders that were read.
	Strays []Stray
}

// Take reads what Emby has, what Jellyfin has, and every folder Emby's videos are in.
func Take(ctx context.Context, o Options) (*Inventory, error) {
	libs, err := o.Emby.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	libs, err = keep(libs, o.Libraries)
	if err != nil {
		return nil, err
	}

	items, err := o.Emby.Items(ctx, libs)
	if err != nil {
		return nil, err
	}

	var thumbs map[string]jellyfin.Video
	if o.Jellyfin != nil {
		if thumbs, err = o.Jellyfin.Thumbnails(ctx); err != nil {
			return nil, fmt.Errorf("reading Jellyfin's thumbnails: %w", err)
		}
	}

	inv := &Inventory{Libraries: libs}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Library == "" {
			continue
		}
		if it.Kind == emby.Show {
			inv.Shows = append(inv.Shows, it)

			continue
		}

		v := &Video{Item: it, Local: o.Paths.Local(it.Path)}
		if jf, ok := thumbs[it.Path]; ok {
			v.Jellyfin = &jf
		}
		seen[it.Path] = true
		inv.Videos = append(inv.Videos, v)
	}
	for path, jf := range thumbs {
		if !seen[path] && emby.LibraryOf(libs, path) != "" {
			inv.JellyfinOnly = append(inv.JellyfinOnly, jf)
		}
	}
	slices.SortFunc(inv.Videos, func(a, b *Video) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(inv.JellyfinOnly, func(a, b jellyfin.Video) int { return strings.Compare(a.Path, b.Path) })

	strays, err := look(ctx, inv.Videos, o)
	if err != nil {
		return nil, err
	}
	inv.Strays = strays

	return inv, nil
}

// keep is the libraries named, or all of them when none are. A name matches whatever its case; one that matches no library is an error listing
// those there are.
func keep(libs []emby.Library, names []string) ([]emby.Library, error) {
	if len(names) == 0 {
		return libs, nil
	}

	var kept []emby.Library
	for _, name := range names {
		i := slices.IndexFunc(libs, func(l emby.Library) bool { return strings.EqualFold(l.Name, strings.TrimSpace(name)) })
		if i < 0 {
			have := make([]string, 0, len(libs))
			for _, l := range libs {
				have = append(have, l.Name)
			}

			return nil, fmt.Errorf("there is no library called %q in Emby: it has %s", name, strings.Join(have, ", "))
		}
		if !slices.ContainsFunc(kept, func(l emby.Library) bool { return l.Name == libs[i].Name }) {
			kept = append(kept, libs[i])
		}
	}

	return kept, nil
}

// look reads every folder the videos are in, once each, for whether each video is there and what BIFs are beside it, and returns the strays in
// them.
func look(ctx context.Context, videos []*Video, o Options) ([]Stray, error) {
	byFolder := ByFolder(videos)
	folders := slices.Sorted(maps.Keys(byFolder))

	var (
		done   atomic.Int64
		mu     sync.Mutex
		strays []Stray
	)
	err := parallel.Each(ctx, len(folders), max(o.Workers, 1), func(ctx context.Context, i int) error {
		found := Folder(ctx, folders[i], byFolder[folders[i]])

		n := done.Add(1)
		mu.Lock()
		strays = append(strays, found...)
		if o.Progress != nil {
			o.Progress(int(n), len(folders))
		}
		mu.Unlock()

		return ctx.Err()
	})
	slices.SortFunc(strays, func(a, b Stray) int { return strings.Compare(a.Path, b.Path) })

	return strays, err
}

// ByFolder is the videos grouped by the folder each is found in.
func ByFolder(videos []*Video) map[string][]*Video {
	byFolder := map[string][]*Video{}
	for _, v := range videos {
		dir := filepath.Dir(v.Local)
		byFolder[dir] = append(byFolder[dir], v)
	}

	return byFolder
}

// sized is a BIF's name as Emby gives it, less the .bif: the video's name, then its width and how many seconds apart.
var sized = regexp.MustCompile(`^(.*)-(\d+)-(\d+)$`)

// Folder reads one folder for whether each of these videos in it is there, and the BIFs beside each, and returns the strays in it.
func Folder(ctx context.Context, dir string, videos []*Video) []Stray {
	entries, err := os.ReadDir(dir)
	if err != nil {
		for _, v := range videos {
			v.Err = err
		}

		return nil
	}

	// names are compared in one form: a share can give back an accented letter as the letter and its accent apart, where Emby has it as one
	byStem := map[string][]*Video{}
	for _, v := range videos {
		v.Found, v.BIFs = false, nil
		stem := norm.NFC.String(Stem(v.Local))
		byStem[stem] = append(byStem[stem], v)
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[norm.NFC.String(e.Name())] = true
	}
	for _, v := range videos {
		v.Found = names[norm.NFC.String(filepath.Base(v.Local))]
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return nil
		}

		stem, ok := strings.CutSuffix(norm.NFC.String(e.Name()), ".bif")
		if !ok || e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		// a sized name whose front is a video's is that video's; failing that, a name that is a video's own is a bare one. A video named as if
		// sized itself, "2020-10-05", is still found, as the bare reading is tried too
		b := BIF{Path: filepath.Join(dir, e.Name()), Name: e.Name()}
		owners := byStem[stem]
		b.Style = Bare
		if m := sized.FindStringSubmatch(stem); len(m) == 4 && len(byStem[m[1]]) > 0 {
			owners, b.Style, b.Named = byStem[m[1]], Sized, m[2]+"-"+m[3]
		}
		if len(owners) == 0 {
			continue
		}

		Read(&b)
		for _, v := range owners {
			v.BIFs = append(v.BIFs, b)
		}
	}

	return strays(dir, entries, videos)
}

// strays is the files in a folder that nothing needs: each BIF none of whose names could be a video's beside it, and each half-written BIF a run
// that was stopped left behind. A video is beside it when it is a file of a video's kind, a folder (a disc's, say), or one of Emby's videos that
// was found there.
func strays(dir string, entries []os.DirEntry, videos []*Video) []Stray {
	present := map[string]bool{}
	for _, v := range videos {
		if v.Found {
			present[norm.NFC.String(Stem(v.Local))] = true
		}
	}
	for _, e := range entries {
		name := norm.NFC.String(e.Name())
		switch {
		case e.IsDir():
			present[name] = true
		case videoExtensions[strings.ToLower(filepath.Ext(name))]:
			present[Stem(name)] = true
		}
	}

	var found []Stray
	for _, e := range entries {
		name := norm.NFC.String(e.Name())
		if e.IsDir() {
			continue
		}

		var why string
		switch stem, ok := strings.CutSuffix(name, ".bif"); {
		case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".ebb-tmp"):
			why = "half written, by a run of ebb that was stopped"
		case ok && !strings.HasPrefix(name, ".") && !slices.ContainsFunc(Stems(stem), func(s string) bool { return present[s] }):
			why = "no video of its name beside it"
		default:
			continue
		}

		s := Stray{Path: filepath.Join(dir, e.Name()), Why: why}
		if fi, err := e.Info(); err == nil {
			s.Bytes, s.Changed = fi.Size(), fi.ModTime()
		}
		// a half-written BIF is only a stray once no run could still be writing it
		if strings.HasSuffix(name, ".ebb-tmp") && time.Since(s.Changed) < leftover {
			continue
		}
		found = append(found, s)
	}

	return found
}

// Read fills in the BIF's size, time and what it holds, or why it could not be read.
func Read(b *BIF) {
	f, err := os.Open(b.Path)
	if err != nil {
		b.Err = err

		return
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		b.Err = err

		return
	}
	b.Bytes, b.Changed = fi.Size(), fi.ModTime()
	b.Info, b.Err = bif.Read(f, fi.Size())
}

// Stems is the names of the videos a BIF could be beside, from its name less .bif: the front of a sized name first, then the whole name, as a bare
// one is named.
func Stems(stem string) []string {
	if m := sized.FindStringSubmatch(stem); m != nil {
		return []string{m[1], stem}
	}

	return []string{stem}
}

// Stem is a video's file name without its extension, which is what the BIFs beside it are named from.
func Stem(path string) string {
	name := filepath.Base(path)

	return strings.TrimSuffix(name, filepath.Ext(name))
}

// NamedWidth is the width a sized BIF's name gives, 0 for a bare one.
func (b BIF) NamedWidth() int {
	w, _, _ := strings.Cut(b.Named, "-")
	n, _ := strconv.Atoi(w)

	return n
}
