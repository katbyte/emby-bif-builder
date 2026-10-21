package trickplay_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/trickplay"
)

// id is a made-up Jellyfin item ID.
const id = "CA2EA61E-9A85-5010-2B76-538C60E3CA05"

// shade is the grey thumbnail k is painted, so each one cut can be told from its neighbours.
func shade(k int) uint8 {
	return uint8(20 + 30*(k%8)) //nolint:gosec // under 256 for every k
}

// lay writes the sheets for these thumbnails where Jellyfin would, each thumbnail painted its own shade and every sheet full size, as Jellyfin's are.
func lay(t *testing.T, root string, i trickplay.Info) {
	t.Helper()

	dir := i.Folder(root)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	per := i.Columns * i.Rows
	for s := range i.Sheets() {
		sheet := image.NewGray(image.Rect(0, 0, i.Columns*i.Width, i.Rows*i.Height))
		for k := range min(per, i.Count-s*per) {
			x, y := (k%i.Columns)*i.Width, (k/i.Columns)*i.Height
			for yy := y; yy < y+i.Height; yy++ {
				for xx := x; xx < x+i.Width; xx++ {
					sheet.SetGray(xx, yy, color.Gray{Y: shade(s*per + k)})
				}
			}
		}

		var b bytes.Buffer
		if err := jpeg.Encode(&b, sheet, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(s)+".jpg"), b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// eight is eight 16x8 thumbnails on sheets of three by two: a full sheet and one holding the last two.
var eight = trickplay.Info{ItemID: id, Width: 16, Height: 8, Columns: 3, Rows: 2, Count: 8, Interval: 10 * time.Second}

// The sheets are where Jellyfin keeps them: under the first two of the ID, then the ID, then the size and the grid, all in lower case.
func TestWhereTheSheetsAre(t *testing.T) {
	t.Parallel()

	i := trickplay.Info{ItemID: id, Width: 640, Height: 360, Columns: 10, Rows: 10, Count: 241}
	want := filepath.Join("x", "ca", "ca2ea61e-9a85-5010-2b76-538c60e3ca05", "640 - 10x10")
	if got := i.Folder("x"); got != want {
		t.Errorf("folder %s, want %s", got, want)
	}
	if i.Sheets() != 3 {
		t.Errorf("241 thumbnails fill %d sheets of 100, want 3", i.Sheets())
	}
}

// Each thumbnail is cut from its sheet in the order taken: across each row, then down, then on to the next sheet.
func TestEachThumbnailIsCutInOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lay(t, root, eight)

	frames, err := trickplay.Cut(t.Context(), root, eight, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 8 {
		t.Fatalf("cut %d thumbnails, want 8", len(frames))
	}

	for k, f := range frames {
		img, err := jpeg.Decode(bytes.NewReader(f))
		if err != nil {
			t.Fatalf("thumbnail %d cannot be read: %v", k, err)
		}
		if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 8 {
			t.Errorf("thumbnail %d is %dx%d, want 16x8", k, b.Dx(), b.Dy())
		}

		// a JPEG comes back near the shade it was given, never exactly
		got, _, _, _ := img.At(8, 4).RGBA()
		if diff := int(got>>8) - int(shade(k)); diff < -6 || diff > 6 {
			t.Errorf("thumbnail %d is shade %d, want about %d: cut from the wrong place", k, got>>8, shade(k))
		}
	}
}

// A sheet that is missing, or too small for what the database says it holds, is said, naming the sheet.
func TestSheetsThatAreWrong(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lay(t, root, eight)
	if err := os.Remove(filepath.Join(eight.Folder(root), "1.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := trickplay.Cut(t.Context(), root, eight, 90); err == nil || !strings.Contains(err.Error(), "1.jpg is missing") {
		t.Errorf("cutting with the second sheet gone gave %v, want it named as missing", err)
	}

	// the database says the thumbnails are twice as wide as the sheets hold
	wide := eight
	wide.Width = 32
	lay(t, root, eight)
	if err := os.Rename(eight.Folder(root), wide.Folder(root)); err != nil {
		t.Fatal(err)
	}
	if _, err := trickplay.Cut(t.Context(), root, wide, 90); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Errorf("cutting sheets too small for the database's thumbnails gave %v, want it said", err)
	}

	if _, err := trickplay.Cut(t.Context(), root, trickplay.Info{ItemID: id}, 90); err == nil {
		t.Error("thumbnails of no size were cut")
	}
}

// When the sheets were last written is the later of their folder's time and the first sheet's.
func TestWhenTheSheetsChanged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lay(t, root, eight)

	later := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(eight.Folder(root), "0.jpg"), later, later); err != nil {
		t.Fatal(err)
	}
	got, err := trickplay.Changed(root, eight)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(later) {
		t.Errorf("changed at %s, want the first sheet's time %s", got, later)
	}

	if _, err := trickplay.Changed(t.TempDir(), eight); err == nil {
		t.Error("sheets that are not there have a time")
	}
}

// An item ID is read from the ID itself in any case, with or without dashes, or from a path into the trickplay folder.
func TestReadingAnItemID(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		id:                                 id,
		strings.ToLower(id):                id,
		"ca2ea61e9a8550102b76538c60e3ca05": id,
		" " + id + " ":                     id,
		"/mnt/data/video/.trickplay/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05":                     id,
		"/mnt/data/video/.trickplay/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05/":                    id,
		"/mnt/data/video/.trickplay/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05/640 - 10x10":         id,
		"/mnt/data/video/.trickplay/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05/640 - 10x10/0.jpg":   id,
		"/mnt/data/video/.trickplay/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05/640 - 10x10/0.jpg/x": "",
		"/mnt/video/tv/Show/Show - 01x01.mkv":                                                    "",
		"ca2ea61e-9a85-5010-2b76-538c60e3ca0":                                                    "",
		"ca2ea61e-9a85-5010-2b76-538c60e3ca0z":                                                   "",
		"ca2ea61e9-a85-5010-2b76-538c60e3ca05":                                                   "",
	} {
		got, ok := trickplay.ItemID(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q reads as %q (%t), want %q", in, got, ok, want)
		}
	}
}
