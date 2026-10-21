package ffmpeg_test

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/ffmpeg"
)

// brightness is how much brighter the made-up video gets each second, so a picture's brightness says when in the video it was taken.
const brightness = 3.5

// clip makes a video of this many seconds that gets brighter by the second, at this size, with pixels of this shape, and a key frame every two
// seconds. A test without ffmpeg to make one is skipped.
func clip(t *testing.T, seconds int, size, sar string) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	path := filepath.Join(t.TempDir(), "clip.mp4")
	src := fmt.Sprintf("color=c=black:size=%s:rate=25:duration=%d,format=gray,geq=lum='T*%g':cb=128:cr=128,setsar=%s", size, seconds, brightness, sar)
	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", src, "-c:v", "libx264", "-g", "50", "-pix_fmt", "yuv420p", "-color_range", "pc", path).CombinedOutput() //nolint:gosec // the test's own clip, made in its own folder
	if err != nil {
		t.Fatalf("making the clip: %v: %s", err, out)
	}

	return path
}

// options is how the tests run ffmpeg: a picture every ten seconds, 640 wide.
func options() ffmpeg.Options {
	return ffmpeg.Options{Program: "ffmpeg", Width: 640, Interval: 10 * time.Second, Quality: 2}
}

// when is the second of the clip a picture was taken at, from how bright it is.
func when(t *testing.T, pic []byte) float64 {
	t.Helper()

	img, err := jpeg.Decode(bytes.NewReader(pic))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()

	return float64((r+g+b)/3>>8) / brightness
}

// Picture k is taken k*10 seconds in, as Emby and Jellyfin count them, and a 65-second video has seven: 0 to 60. Taking key frames alone, which come
// every two seconds here, gives the same.
func TestAPictureEveryTenSeconds(t *testing.T) {
	t.Parallel()

	video := clip(t, 65, "64x36", "1")
	for _, keyframes := range []bool{false, true} {
		o := options()
		o.KeyFrames = keyframes

		frames, err := ffmpeg.Frames(t.Context(), o, video)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 7 {
			t.Fatalf("key frames %t: took %d pictures, want 7", keyframes, len(frames))
		}
		for k, f := range frames {
			if at := when(t, f); at < float64(k*10)-1.5 || at > float64(k*10)+0.5 {
				t.Errorf("key frames %t: picture %d is from %.1fs in, want %ds", keyframes, k, at, k*10)
			}
		}
	}
}

// No picture is wider than the video is shown, and a video whose pixels are not square is shown at its proper shape: a PAL DVD's 720x576 is 4:3,
// where taking its pixels as square would squash it.
func TestThePicturesSize(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		size, sar string
		w, h      int
	}{
		{"1280x720", "1", 640, 360},
		{"320x240", "1", 320, 240},
		{"720x576", "16/15", 640, 480},
		{"544x576", "64/45", 640, 476},
	} {
		frames, err := ffmpeg.Frames(t.Context(), options(), clip(t, 3, c.size, c.sar))
		if err != nil {
			t.Fatal(err)
		}

		cfg, err := jpeg.DecodeConfig(bytes.NewReader(frames[0]))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != c.w || cfg.Height != c.h {
			t.Errorf("%s with pixels %s gives pictures %dx%d, want %dx%d", c.size, c.sar, cfg.Width, cfg.Height, c.w, c.h)
		}
	}
}

// A video ffmpeg cannot read says what ffmpeg said, and a program that is not there says how to get one.
func TestWhenFFmpegFails(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	_, err := ffmpeg.Frames(t.Context(), options(), filepath.Join(t.TempDir(), "nothing.mkv"))
	if err == nil || !strings.Contains(err.Error(), "nothing.mkv") {
		t.Errorf("a video that is not there gave %v, want ffmpeg's word on it", err)
	}

	if err := ffmpeg.Check(t.Context(), "ffmpeg"); err != nil {
		t.Errorf("the installed ffmpeg fails its check: %v", err)
	}
	if err := ffmpeg.Check(t.Context(), "no-such-ffmpeg"); err == nil || !strings.Contains(err.Error(), "install ffmpeg") {
		t.Errorf("a missing ffmpeg gave %v, want it said how to get one", err)
	}
}

// A video card decodes when asked, the frames then being brought back from it before they are scaled for the pictures.
func TestDecodingOnAVideoCard(t *testing.T) {
	t.Parallel()

	o := options()
	o.HWAccel, o.KeyFrames = "cuda", true
	args := o.Args("/v.mkv", "/tmp/x")
	for _, want := range []string{"-hwaccel cuda -hwaccel_output_format cuda", "-skip_frame nokey -i /v.mkv", "scale_cuda=", "hwdownload"} {
		if !strings.Contains(strings.Join(args, " "), want) {
			t.Errorf("ffmpeg is told %q, which lacks %q", args, want)
		}
	}
	if i := slices.Index(args, "-i"); i < 0 || args[i+1] != "/v.mkv" {
		t.Errorf("ffmpeg is not told the video: %q", args)
	}
}
