// Package bif writes and reads BIF files: the preview pictures Emby shows while scrubbing and for chapters, one picture for every few seconds of a
// video, as Roku first laid them out.
//
// A BIF is a 64-byte header, an index of where each picture starts, then the pictures as JPEGs back to back. Numbers are little-endian.
package bif

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"math"
	"strings"
	"time"
)

// Magic is how every BIF starts.
const Magic = "\x89BIF\r\n\x1a\n"

const (
	headerSize = 64
	entrySize  = 8
	// end is the time the index's last entry has, which says where the last picture ends rather than where another starts.
	end = math.MaxUint32
	// maxFrames is more pictures than any video has, one every second for a fortnight, so a count beyond it is a file that is not a BIF.
	maxFrames = 1_209_600
	// firstPeek is how much of the first picture is read for its size: far more than a JPEG's header takes.
	firstPeek = 64 << 10
)

// notePrefix starts the comment ebb puts in the first picture of each BIF it makes, so it can tell its own BIFs from Emby's.
const notePrefix = "emby-bif-builder: "

// Write writes a BIF of these pictures, one for every interval of the video, to w. Each picture is a JPEG.
func Write(w io.Writer, interval time.Duration, frames [][]byte) error {
	if len(frames) == 0 || len(frames) > maxFrames {
		return fmt.Errorf("a BIF holds from 1 to %d pictures, not %d", maxFrames, len(frames))
	}
	if interval < time.Millisecond || interval.Milliseconds() > math.MaxUint32 {
		return fmt.Errorf("a picture every %s cannot be written in a BIF", interval)
	}

	offset := uint64(headerSize + entrySize*(len(frames)+1))
	for i, f := range frames {
		if !isJPEG(f) {
			return fmt.Errorf("picture %d is not a JPEG", i)
		}
		offset += uint64(len(f))
	}
	if offset > math.MaxUint32 {
		return fmt.Errorf("%d pictures take %d bytes, more than a BIF can hold", len(frames), offset)
	}

	head := make([]byte, headerSize, headerSize+entrySize*(len(frames)+1))
	copy(head, Magic)
	// bytes 8 to 11 are the version, 0
	binary.LittleEndian.PutUint32(head[12:], u32(len(frames)))
	binary.LittleEndian.PutUint32(head[16:], u32(interval.Milliseconds()))

	offset = uint64(headerSize + entrySize*(len(frames)+1))
	for i, f := range frames {
		head = binary.LittleEndian.AppendUint32(head, u32(i))
		head = binary.LittleEndian.AppendUint32(head, u32(offset))
		offset += uint64(len(f))
	}
	head = binary.LittleEndian.AppendUint32(head, end)
	head = binary.LittleEndian.AppendUint32(head, u32(offset))

	if _, err := w.Write(head); err != nil {
		return err
	}
	for _, f := range frames {
		if _, err := w.Write(f); err != nil {
			return err
		}
	}

	return nil
}

// Info is what a BIF holds, from its header, its index and its first picture.
type Info struct {
	// Frames is how many pictures it holds, and Interval how far apart in the video they were taken.
	Frames   int
	Interval time.Duration
	// Width and Height are the first picture's size, which every picture in a BIF shares.
	Width  int
	Height int
	// Note is what ebb wrote in the first picture when it made the BIF, "" for one it did not make.
	Note string
}

// Read reads what the BIF of this size holds. A file that is not a BIF, or one cut short, is an error saying which.
func Read(r io.ReaderAt, size int64) (Info, error) {
	head := make([]byte, headerSize)
	if _, err := r.ReadAt(head, 0); err != nil {
		if errors.Is(err, io.EOF) {
			return Info{}, errors.New("not a BIF: it is too short to be one")
		}

		return Info{}, err
	}
	if string(head[:8]) != Magic {
		return Info{}, errors.New("not a BIF: it does not start as one does")
	}

	n := int(binary.LittleEndian.Uint32(head[12:]))
	if n < 1 || n > maxFrames {
		return Info{}, fmt.Errorf("not a BIF: it says it holds %d pictures", n)
	}
	unit := time.Duration(binary.LittleEndian.Uint32(head[16:])) * time.Millisecond
	// a BIF that gives no unit counts in seconds
	if unit == 0 {
		unit = time.Second
	}

	index := make([]byte, entrySize*(n+1))
	if _, err := r.ReadAt(index, headerSize); err != nil {
		if errors.Is(err, io.EOF) {
			return Info{}, fmt.Errorf("cut short: it says it holds %d pictures but ends in the middle of their index", n)
		}

		return Info{}, err
	}

	first := int64(binary.LittleEndian.Uint32(index[4:]))
	prev, at := first, binary.LittleEndian.Uint32(index[0:])
	for i := 1; i <= n; i++ {
		off := int64(binary.LittleEndian.Uint32(index[i*entrySize+4:]))
		if off < prev {
			return Info{}, fmt.Errorf("broken: picture %d starts before picture %d", i, i-1)
		}
		prev = off

		if i < n {
			next := binary.LittleEndian.Uint32(index[i*entrySize:])
			if next <= at {
				return Info{}, fmt.Errorf("broken: picture %d is not later in the video than picture %d", i, i-1)
			}
			at = next
		}
	}
	if last := binary.LittleEndian.Uint32(index[n*entrySize:]); last != end {
		return Info{}, errors.New("broken: its index does not end as a BIF's does")
	}
	if first < int64(headerSize+len(index)) {
		return Info{}, errors.New("broken: its first picture starts inside its index")
	}
	if prev > size {
		return Info{}, fmt.Errorf("cut short: its pictures run to byte %d but the file ends at %d", prev, size)
	}

	info := Info{Frames: n, Interval: unit}
	if n > 1 {
		info.Interval = time.Duration(binary.LittleEndian.Uint32(index[entrySize:])-binary.LittleEndian.Uint32(index[0:])) * unit
	}

	pic := make([]byte, min(int64(firstPeek), int64(binary.LittleEndian.Uint32(index[entrySize+4:]))-first))
	if _, err := r.ReadAt(pic, first); err != nil && !errors.Is(err, io.EOF) {
		return Info{}, err
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(pic))
	if err != nil {
		return Info{}, fmt.Errorf("broken: its first picture cannot be read: %w", err)
	}
	info.Width, info.Height = cfg.Width, cfg.Height
	info.Note = NoteOf(pic)

	return info, nil
}

// Pictures reads every picture of the BIF of this size and says which is the first that cannot be shown, and why. It reads all of the file, where
// Read reads only its start.
func Pictures(r io.ReaderAt, size int64) error {
	// the start is read first, so a large file that is not a BIF is never read whole
	info, err := Read(r, size)
	if err != nil {
		return err
	}

	all := make([]byte, size)
	if _, err := r.ReadAt(all, 0); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	index := all[headerSize : headerSize+entrySize*(info.Frames+1)]
	for i := range info.Frames {
		from, to := binary.LittleEndian.Uint32(index[i*entrySize+4:]), binary.LittleEndian.Uint32(index[(i+1)*entrySize+4:])
		if from == to {
			return fmt.Errorf("broken: picture %d is empty", i)
		}

		img, err := jpeg.Decode(bytes.NewReader(all[from:to]))
		if err != nil {
			return fmt.Errorf("broken: picture %d cannot be shown: %w", i, err)
		}
		if b := img.Bounds(); b.Dx() != info.Width || b.Dy() != info.Height {
			return fmt.Errorf("broken: picture %d is %dx%d, where the first is %dx%d", i, b.Dx(), b.Dy(), info.Width, info.Height)
		}
	}

	return nil
}

// WithNote is the JPEG with a comment saying who made it, where a JPEG's comments go: straight after the two bytes that start it. Anything that
// shows the picture passes over the comment.
func WithNote(pic []byte, note string) []byte {
	text := notePrefix + note
	if !isJPEG(pic) || len(text) > math.MaxUint16-2 {
		return pic
	}

	out := make([]byte, 0, len(pic)+4+len(text))
	out = append(out, pic[:2]...)
	out = append(out, 0xff, 0xfe)
	out = binary.BigEndian.AppendUint16(out, uint16(len(text)+2)) //nolint:gosec // the note's length is checked to fit above
	out = append(out, text...)

	return append(out, pic[2:]...)
}

// NoteOf is the note ebb wrote in a JPEG, "" when it has none: the first comment that starts as ebb's do, among those before the picture itself.
func NoteOf(pic []byte) string {
	if !isJPEG(pic) {
		return ""
	}

	for i := 2; i+4 <= len(pic) && pic[i] == 0xff; {
		marker := pic[i+1]
		// the picture itself starts here, and no comment comes after
		if marker == 0xda {
			return ""
		}

		n := int(binary.BigEndian.Uint16(pic[i+2:]))
		if n < 2 || i+2+n > len(pic) {
			return ""
		}
		if marker == 0xfe {
			if text, ok := strings.CutPrefix(string(pic[i+4:i+2+n]), notePrefix); ok {
				return text
			}
		}
		i += 2 + n
	}

	return ""
}

// u32 is n as the 32 bits a BIF keeps it in, for a number already checked to fit.
func u32[T int | int64 | uint64](n T) uint32 {
	return uint32(n)
}

// isJPEG says whether the bytes start as a JPEG does.
func isJPEG(pic []byte) bool {
	return len(pic) >= 4 && pic[0] == 0xff && pic[1] == 0xd8 && pic[2] == 0xff
}
