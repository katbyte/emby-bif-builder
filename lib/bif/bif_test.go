package bif_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
)

// picture is a JPEG of this size in one colour, the shade telling one picture from another.
func picture(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()

	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade
	}

	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

// The file is laid out as Roku's spec and Emby's own files have it: the header, one entry a picture saying when it was taken and where it starts,
// a last entry saying where the pictures end, then the pictures.
func TestTheLayoutIsRokus(t *testing.T) {
	t.Parallel()

	frames := [][]byte{picture(t, 32, 18, 10), picture(t, 32, 18, 20), picture(t, 32, 18, 30)}

	var b bytes.Buffer
	if err := bif.Write(&b, 10*time.Second, frames); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()

	if string(raw[:8]) != bif.Magic {
		t.Errorf("it starts % x, want % x", raw[:8], bif.Magic)
	}
	u32 := func(at int) uint32 { return binary.LittleEndian.Uint32(raw[at:]) }
	if u32(8) != 0 || u32(12) != 3 || u32(16) != 10000 {
		t.Errorf("version %d, pictures %d, unit %d ms: want 0, 3 and 10000", u32(8), u32(12), u32(16))
	}
	for i := 20; i < 64; i++ {
		if raw[i] != 0 {
			t.Fatalf("byte %d is %d, want the rest of the header empty", i, raw[i])
		}
	}

	at := 64 + 8*4
	for i, f := range frames {
		if int(u32(64+8*i)) != i || int(u32(68+8*i)) != at {
			t.Errorf("entry %d is %d at %d, want %d at %d", i, u32(64+8*i), u32(68+8*i), i, at)
		}
		if !bytes.Equal(raw[at:at+len(f)], f) {
			t.Errorf("picture %d is not where its entry says", i)
		}
		at += len(f)
	}
	if u32(64+8*3) != 0xffffffff || int(u32(68+8*3)) != at || at != len(raw) {
		t.Errorf("the last entry is %x at %d, want ffffffff at the end of the file, %d", u32(64+8*3), u32(68+8*3), len(raw))
	}
}

// What is written reads back: how many pictures, how far apart, their size, and the note in the first.
func TestWhatIsWrittenReadsBack(t *testing.T) {
	t.Parallel()

	first := bif.WithNote(picture(t, 64, 36, 50), "jellyfin 1234")
	frames := [][]byte{first, picture(t, 64, 36, 60)}

	var b bytes.Buffer
	if err := bif.Write(&b, 10*time.Second, frames); err != nil {
		t.Fatal(err)
	}

	info, err := bif.Read(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	want := bif.Info{Frames: 2, Interval: 10 * time.Second, Width: 64, Height: 36, Note: "jellyfin 1234"}
	if info != want {
		t.Errorf("read %+v, want %+v", info, want)
	}

	// the note does not stop the picture being shown
	if _, err := jpeg.Decode(bytes.NewReader(first)); err != nil {
		t.Errorf("the picture with a note cannot be read: %v", err)
	}
}

// A BIF ebb did not make has no note, and one that gives no unit counts in seconds, as the spec says.
func TestABIFFromElsewhere(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := bif.Write(&b, 5*time.Second, [][]byte{picture(t, 16, 16, 1), picture(t, 16, 16, 2)}); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	// a unit of 0 with timestamps 0 and 1 is one second apart, the way an old Roku file says it
	binary.LittleEndian.PutUint32(raw[16:], 0)

	info, err := bif.Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Interval != time.Second || info.Note != "" || info.Frames != 2 {
		t.Errorf("read %+v, want 2 pictures a second apart and no note", info)
	}
}

// A file that is not a BIF, or is one cut short or broken, says so rather than giving a size.
func TestWhatIsNotABIF(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := bif.Write(&b, 10*time.Second, [][]byte{picture(t, 16, 16, 1), picture(t, 16, 16, 2)}); err != nil {
		t.Fatal(err)
	}
	good := b.Bytes()

	backwards := bytes.Clone(good)
	binary.LittleEndian.PutUint32(backwards[72:], 0)

	b.Reset()
	if err := bif.Write(&b, 10*time.Second, [][]byte{picture(t, 16, 16, 1), picture(t, 16, 16, 2), picture(t, 16, 16, 3)}); err != nil {
		t.Fatal(err)
	}
	laterBackwards := bytes.Clone(b.Bytes())
	binary.LittleEndian.PutUint32(laterBackwards[80:], 1)

	none := bytes.Clone(good)
	binary.LittleEndian.PutUint32(none[12:], 0)

	for name, c := range map[string]struct {
		raw  []byte
		want string
	}{
		"empty":         {nil, "too short"},
		"a JPEG":        {picture(t, 16, 16, 1), "does not start as one does"},
		"cut short":     {good[:len(good)-10], "cut short"},
		"index cut off": {good[:70], "middle of their index"},
		"no pictures":   {none, "holds 0 pictures"},
		"out of order":  {backwards, "picture 1 is not later in the video than picture 0"},
		"later on":      {laterBackwards, "picture 2 is not later in the video than picture 1"},
	} {
		if _, err := bif.Read(bytes.NewReader(c.raw), int64(len(c.raw))); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: read gave %v, want an error saying %q", name, err, c.want)
		}
	}
}

// Reading every picture finds one that cannot be shown, one with nothing in it, and one of another size, which reading the start of the file does not.
func TestEveryPicture(t *testing.T) {
	t.Parallel()

	write := func(pics ...[]byte) []byte {
		var b bytes.Buffer
		if err := bif.Write(&b, 10*time.Second, pics); err != nil {
			t.Fatal(err)
		}

		return b.Bytes()
	}
	good, cut := picture(t, 16, 16, 1), picture(t, 16, 16, 2)

	// the second picture's entry is moved on to where the third starts, so the second holds nothing
	empty := write(good, good, good)
	binary.LittleEndian.PutUint32(empty[64+8+4:], binary.LittleEndian.Uint32(empty[64+16+4:]))

	fine := write(good, good, good)
	if err := bif.Pictures(bytes.NewReader(fine), int64(len(fine))); err != nil {
		t.Errorf("a good BIF gave %v", err)
	}
	for name, c := range map[string]struct {
		raw  []byte
		want string
	}{
		"cut short":    {write(good, cut[:len(cut)/2], good), "picture 1 cannot be shown"},
		"empty":        {empty, "picture 1 is empty"},
		"another size": {write(good, good, picture(t, 32, 16, 3)), "picture 2 is 32x16, where the first is 16x16"},
		"not a BIF":    {good, "does not start as one does"},
	} {
		if _, err := bif.Read(bytes.NewReader(c.raw), int64(len(c.raw))); err != nil && name != "not a BIF" {
			t.Errorf("%s: reading the start gave %v, want it read as it is not looked at", name, err)
		}
		if err := bif.Pictures(bytes.NewReader(c.raw), int64(len(c.raw))); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: reading every picture gave %v, want an error saying %q", name, err, c.want)
		}
	}
}

// Only JPEGs go in, and at least one.
func TestWhatCannotBeWritten(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := bif.Write(&b, 10*time.Second, nil); err == nil {
		t.Error("a BIF of no pictures was written")
	}
	if err := bif.Write(&b, 10*time.Second, [][]byte{[]byte("not a picture")}); err == nil {
		t.Error("a BIF of something that is not a JPEG was written")
	}
	if err := bif.Write(&b, 0, [][]byte{picture(t, 8, 8, 1)}); err == nil {
		t.Error("a BIF of pictures taken no time apart was written")
	}
}

// The note is found among the JPEG's other parts, and a picture with none, or a comment of someone else's, has none.
func TestTheNote(t *testing.T) {
	t.Parallel()

	pic := picture(t, 8, 8, 1)
	if n := bif.NoteOf(pic); n != "" {
		t.Errorf("a plain picture has note %q", n)
	}
	if n := bif.NoteOf(bif.WithNote(pic, "ffmpeg 640")); n != "ffmpeg 640" {
		t.Errorf("the note reads %q, want ffmpeg 640", n)
	}

	// someone else's comment comes first, and ebb's after it is still found
	other := append([]byte{0xff, 0xd8, 0xff, 0xfe, 0x00, 0x07}, "hello"...)
	other = append(other, bif.WithNote(pic, "jellyfin x")[2:]...)
	if n := bif.NoteOf(other); n != "jellyfin x" {
		t.Errorf("after another comment the note reads %q, want jellyfin x", n)
	}

	if got := bif.WithNote([]byte("not a picture"), "x"); string(got) != "not a picture" {
		t.Errorf("a note was put in something that is not a JPEG: %q", got)
	}
	if n := bif.NoteOf([]byte("not a picture")); n != "" {
		t.Errorf("something that is not a JPEG has note %q", n)
	}
}
