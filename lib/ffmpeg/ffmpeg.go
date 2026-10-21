// Package ffmpeg takes the pictures for a BIF from a video itself, with ffmpeg: one every few seconds, at a set width.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Options is how ffmpeg is run.
type Options struct {
	// Program is the ffmpeg to run: a name looked up on the PATH, or a path to one.
	Program string
	// HWAccel is the video card to decode with, "cuda" for an NVIDIA one, or "" to decode on the processor.
	HWAccel string
	// KeyFrames decodes only a video's key frames, which is much faster: each picture is then the key frame at or before its time, rather than the
	// frame at its time.
	KeyFrames bool
	// Width is how wide the pictures are, unless the video is narrower: no picture is made wider than the video is shown.
	Width int
	// Interval is how far apart in the video the pictures are taken.
	Interval time.Duration
	// Quality is ffmpeg's JPEG quality, 2 the best and 31 the worst.
	Quality int
	// Temp is where the pictures are put while they are taken, the system's own temporary folder when "".
	Temp string
}

// HWAccels is the video cards a picture can be decoded on, as the --hwaccel setting spells them.
var HWAccels = []string{"", "cuda"}

// Args is what ffmpeg is told, to write the pictures of video into dir.
//
// A picture is taken for each step of the interval from the start, the frame at or before that time, so picture k is k*Interval in as Emby and
// Jellyfin count it. Its width is the lesser of Width and the video's own width as shown, which counts its pixels' shape, and its height follows.
func (o Options) Args(video, dir string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if o.HWAccel != "" {
		args = append(args, "-hwaccel", o.HWAccel, "-hwaccel_output_format", o.HWAccel)
	}
	if o.KeyFrames {
		args = append(args, "-skip_frame", "nokey")
	}

	// the rate filter's own rounding takes each picture from half a step later, so it is told to round up: the frame at or before each step
	every := strconv.FormatFloat(o.Interval.Seconds(), 'f', -1, 64)
	size := fmt.Sprintf("w='trunc(min(%d\\,iw*sar)/2)*2':h='trunc(ow/dar/2)*2'", o.Width)
	filter := "fps=1/" + every + ":round=up,scale=" + size + ",setsar=1"
	if o.HWAccel == "cuda" {
		filter = "fps=1/" + every + ":round=up,scale_cuda=" + size + ":format=yuv420p,hwdownload,format=yuv420p,setsar=1"
	}

	return append(args, "-i", video, "-an", "-sn", "-dn", "-map", "0:V:0", "-vf", filter, "-fps_mode", "passthrough", "-c:v", "mjpeg", "-q:v", strconv.Itoa(o.Quality), "-pix_fmt", "yuvj420p", "-f", "image2", filepath.Join(dir, "%06d.jpg"))
}

// Frames takes the pictures of the video, in order. What ffmpeg says when it fails is part of the error.
func Frames(ctx context.Context, o Options, video string) ([][]byte, error) {
	dir, err := os.MkdirTemp(o.Temp, "ebb-frames-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, o.Program, o.Args(video, dir)...) //nolint:gosec // the program is the user's own setting, and the video one Emby has
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("ffmpeg failed on %s: %w: %s", video, err, lastLines(stderr.String(), 5))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jpg") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("ffmpeg took no pictures of %s: %s", video, lastLines(stderr.String(), 5))
	}

	frames := make([][]byte, 0, len(names))
	for _, n := range names {
		pic, err := os.ReadFile(filepath.Join(dir, n)) //nolint:gosec // a picture ffmpeg wrote into ebb's own temporary folder
		if err != nil {
			return nil, err
		}
		frames = append(frames, pic)
	}

	return frames, nil
}

// Check runs the program to see that it is an ffmpeg that can be run, and says what to do when it is not.
func Check(ctx context.Context, program string) error {
	out, err := exec.CommandContext(ctx, program, "-hide_banner", "-version").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("there is no %s to run: install ffmpeg, or point --ffmpeg at one", program)
		}

		return fmt.Errorf("%s does not run: %w", program, err)
	}
	if !bytes.HasPrefix(out, []byte("ffmpeg version")) {
		return fmt.Errorf("%s does not say it is ffmpeg", program)
	}

	return nil
}

// lastLines is the last few lines of what a program said, on one line, for an error.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return "it said nothing"
	}

	return strings.Join(lines, " / ")
}
