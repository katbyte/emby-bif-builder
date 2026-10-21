// Package trickplay reads Jellyfin's thumbnail sheets: the grids of small pictures Jellyfin takes of a video every few seconds and keeps in its
// trickplay folder, under the video's item ID.
package trickplay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Info is one set of a video's thumbnails, as Jellyfin's database describes it.
type Info struct {
	// ItemID is the video's ID as Jellyfin's database spells it: upper case, with dashes.
	ItemID string
	// Width and Height are each thumbnail's size.
	Width  int
	Height int
	// Columns and Rows are how many thumbnails each sheet holds across and down.
	Columns int
	Rows    int
	// Count is how many thumbnails there are, and Interval how far apart in the video they were taken: thumbnail k is k*Interval in.
	Count    int
	Interval time.Duration
}

// Folder is where the sheets are under Jellyfin's trickplay folder: <first two of the ID>/<ID>/<width> - <columns>x<rows>, in lower case.
func (i Info) Folder(root string) string {
	id := strings.ToLower(i.ItemID)

	return filepath.Join(root, id[:min(2, len(id))], id, fmt.Sprintf("%d - %dx%d", i.Width, i.Columns, i.Rows))
}

// Sheets is how many sheets the thumbnails fill.
func (i Info) Sheets() int {
	per := i.Columns * i.Rows
	if per <= 0 {
		return 0
	}

	return (i.Count + per - 1) / per
}

// check says what is wrong with an Info that could not have come from Jellyfin.
func (i Info) check() error {
	switch {
	case len(i.ItemID) < 2:
		return fmt.Errorf("item ID %q is too short", i.ItemID)
	case i.Width <= 0 || i.Height <= 0:
		return fmt.Errorf("thumbnails %dx%d have no size", i.Width, i.Height)
	case i.Columns <= 0 || i.Rows <= 0:
		return fmt.Errorf("sheets of %dx%d thumbnails hold none", i.Columns, i.Rows)
	case i.Count <= 0:
		return errors.New("there are no thumbnails")
	case i.Interval <= 0:
		return fmt.Errorf("thumbnails %s apart", i.Interval)
	}

	return nil
}

// Changed is when the sheets were last written: the later of their folder's time and the first sheet's, which Jellyfin writes again whenever it
// makes them again. A time other than the one a BIF was cut at says they have been made again since.
func Changed(root string, i Info) (time.Time, error) {
	dir := i.Folder(root)
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, err
	}

	newest := info.ModTime()
	if fi, err := os.Stat(filepath.Join(dir, "0.jpg")); err == nil && fi.ModTime().After(newest) {
		newest = fi.ModTime()
	}

	return newest, nil
}

// Cut reads the sheets under Jellyfin's trickplay folder and returns each thumbnail as a JPEG of its own, in the order they were taken. A sheet that
// is missing, or not the size the database says, is an error naming it.
func Cut(ctx context.Context, root string, i Info, quality int) ([][]byte, error) {
	if err := i.check(); err != nil {
		return nil, err
	}

	dir := i.Folder(root)
	per := i.Columns * i.Rows
	frames := make([][]byte, 0, i.Count)
	for s := range i.Sheets() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		path := filepath.Join(dir, strconv.Itoa(s)+".jpg")
		sheet, err := readSheet(path)
		if err != nil {
			return nil, err
		}

		// the last sheet holds what is left, and may be blank after it
		n := min(per, i.Count-s*per)
		rows := (n + i.Columns - 1) / i.Columns
		if b := sheet.Bounds(); b.Dx() < i.Columns*i.Width || b.Dy() < rows*i.Height {
			return nil, fmt.Errorf("%s is %dx%d, too small for %d rows of %d %dx%d thumbnails", path, b.Dx(), b.Dy(), rows, i.Columns, i.Width, i.Height)
		}

		for k := range n {
			x, y := sheet.Bounds().Min.X+(k%i.Columns)*i.Width, sheet.Bounds().Min.Y+(k/i.Columns)*i.Height
			pic, err := encode(sheet.SubImage(image.Rect(x, y, x+i.Width, y+i.Height)), quality)
			if err != nil {
				return nil, fmt.Errorf("%s: thumbnail %d: %w", path, s*per+k, err)
			}
			frames = append(frames, pic)
		}
	}

	return frames, nil
}

// subImager is every image the JPEG reader returns: a picture a part of which can be taken without copying it.
type subImager interface {
	image.Image
	SubImage(r image.Rectangle) image.Image
}

// readSheet reads one sheet.
func readSheet(path string) (subImager, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a sheet in the trickplay folder the user named
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing", path)
		}

		return nil, err
	}

	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read as a picture: %w", path, err)
	}
	sheet, ok := img.(subImager)
	if !ok {
		return nil, fmt.Errorf("%s is a kind of picture that cannot be cut", path)
	}

	return sheet, nil
}

// encode writes one thumbnail as a JPEG.
func encode(img image.Image, quality int) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

// ItemID reads a Jellyfin item ID out of what it was given: the ID, with or without its dashes, or a path into the trickplay folder (the ID's folder,
// a sheet folder in it, or a sheet). The ID comes back as Jellyfin's database spells it.
func ItemID(s string) (string, bool) {
	s = strings.TrimRight(filepath.ToSlash(strings.TrimSpace(s)), "/")
	parts := strings.Split(s, "/")
	for i := len(parts) - 1; i >= 0 && i >= len(parts)-3; i-- {
		if id, ok := parseID(parts[i]); ok {
			return id, true
		}
	}

	return "", false
}

// parseID is the ID spelled as Jellyfin's database does, from 32 hex digits with or without the four dashes.
func parseID(s string) (string, bool) {
	hex := strings.ReplaceAll(s, "-", "")
	if len(hex) != 32 || (len(s) != 32 && len(s) != 36) {
		return "", false
	}
	for _, r := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", false
		}
	}
	if len(s) == 36 && (s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-') {
		return "", false
	}

	hex = strings.ToUpper(hex)

	return hex[:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:], true
}
