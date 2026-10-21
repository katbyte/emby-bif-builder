//go:build acceptance

package acceptance

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/emby-bif-builder/lib/servertest"
)

// result is what a run of the program printed, what it said went wrong, and how it ended.
type result struct {
	out, errs string
	code      int
}

// run runs the program in dir with these settings in its environment, and nothing else of whoever runs the test's.
func run(t *testing.T, dir string, env map[string]string, args ...string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), program, args...) //nolint:gosec // the program under test
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()

	r := result{out: out.String(), errs: errs.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	case err != nil:
		t.Fatalf("running %s: %v", program, err)
	}

	return r
}

// servers lays out a made-up Emby and Jellyfin, and gives the settings that point the program at them and where the test finds their three
// videos: one Jellyfin has thumbnails of and that has no BIF, one with Emby's own narrow BIF and an old bare one, and one Jellyfin has not got to.
func servers(t *testing.T) (env map[string]string, videos []string) {
	t.Helper()

	s := servertest.New(t)
	s.Library("TV", "tvshows", "tv")
	fresh := s.Episode("tv/Show/Show - 01x01.mkv")
	s.Thumbnails(fresh, 64, 36, 12)
	narrow := s.Episode("tv/Show/Show - 01x02.mkv")
	s.Thumbnails(narrow, 64, 36, 12)
	s.BIF(narrow, "Show - 01x02-320-10.bif", 32, 18, 12)
	s.BIF(narrow, "Show - 01x02.bif", 32, 18, 12)
	later := s.Episode("tv/Show/Show - 01x03.mkv")
	s.Done()

	env = map[string]string{
		"EMBY_BIF_BUILDER_EMBY_DB_PATH":      s.EmbyDB,
		"EMBY_BIF_BUILDER_JF_DB_PATH":        s.JellyfinDB,
		"EMBY_BIF_BUILDER_JF_TRICKPLAY_PATH": s.Trickplay,
		"EMBY_BIF_BUILDER_MEDIA_PATHS":       s.MediaPaths(),
		"EMBY_BIF_BUILDER_RECORD":            filepath.Join(t.TempDir(), "changes.tsv"),
	}

	return env, []string{s.Local(fresh), s.Local(narrow), s.Local(later)}
}

// The program says what it is, and ends well.
func TestItSaysWhatItIs(t *testing.T) {
	r := run(t, t.TempDir(), nil, "version")
	if r.code != 0 || !strings.HasPrefix(r.out, "ebb ") {
		t.Errorf("version said %q and ended %d, want ebb and its version", r.out, r.code)
	}
}

// A run that cannot be done ends badly, saying why and which settings it needs.
func TestARunThatCannotBeDone(t *testing.T) {
	r := run(t, t.TempDir(), nil, "copy")
	if r.code != 1 || !strings.Contains(r.errs, "EMBY_BIF_BUILDER_EMBY_DB_PATH") {
		t.Errorf("copy with nothing set ended %d saying %q, want 1 and the settings it needs", r.code, r.errs)
	}
}

// Copy is tried, done, and done again with --upgrade, from settings in a settings file, and a run after finds nothing to do. Each change is in the
// record, and a dry run changes nothing.
func TestCopyAndUpgrade(t *testing.T) {
	env, videos := servers(t)

	// the settings file spells each setting as the environment does, less the prefix
	dir := t.TempDir()
	var file strings.Builder
	for k, v := range env {
		file.WriteString(strings.TrimPrefix(k, "EMBY_BIF_BUILDER_") + "=" + v + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, ".emby-bif-builder"), []byte(file.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	r := run(t, dir, nil, "copy", "--upgrade", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "would make 1, would upgrade 1, would remake 0") {
		t.Fatalf("the dry run ended %d:\n%s%s", r.code, r.out, r.errs)
	}
	if _, err := os.Stat(strings.TrimSuffix(videos[0], ".mkv") + "-320-10.bif"); err == nil {
		t.Error("the dry run made a BIF")
	}

	r = run(t, dir, nil, "copy")
	if r.code != 0 || !strings.Contains(r.out, "made 1, upgraded 0, remade 0, failed 0") {
		t.Fatalf("copy ended %d:\n%s%s", r.code, r.out, r.errs)
	}
	r = run(t, dir, nil, "copy", "--upgrade")
	if r.code != 0 || !strings.Contains(r.out, "made 0, upgraded 1, remade 0, failed 0") || !strings.Contains(r.out, "removed 1 old BIFs") {
		t.Fatalf("copy --upgrade ended %d:\n%s%s", r.code, r.out, r.errs)
	}

	for _, v := range videos[:2] {
		if info := servertest.Read(t, strings.TrimSuffix(v, ".mkv")+"-320-10.bif"); info.Width != 64 || info.Frames != 12 {
			t.Errorf("%s's BIF holds %+v, want Jellyfin's 12 pictures 64 wide", v, info)
		}
	}
	if _, err := os.Stat(strings.TrimSuffix(videos[1], ".mkv") + ".bif"); err == nil {
		t.Error("the old bare BIF is still there")
	}

	raw, err := os.ReadFile(env["EMBY_BIF_BUILDER_RECORD"])
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 3 {
		t.Errorf("the record holds %q, want a BIF written, one replaced and one removed", lines)
	}

	r = run(t, dir, nil, "copy", "--upgrade")
	if r.code != 0 || !strings.Contains(r.out, "made 0, upgraded 0, remade 0, failed 0") {
		t.Errorf("a run after did more, ending %d:\n%s%s", r.code, r.out, r.errs)
	}

	r = run(t, dir, nil, "scan")
	if r.code != 0 || !strings.Contains(r.out, "made by:   ebb, from Jellyfin: 2") {
		t.Errorf("stats ended %d:\n%s%s", r.code, r.out, r.errs)
	}
}

// Build makes a BIF from the video Jellyfin has not got to, with ffmpeg, and leaves the others to copy.
func TestBuild(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("the live tests need ffmpeg")
	}

	env, videos := servers(t)
	later := videos[2]
	if err := os.Remove(later); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=35", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "matroska", later).CombinedOutput() //nolint:gosec // the test's own video
	if err != nil {
		t.Fatalf("making the video: %v: %s", err, out)
	}

	r := run(t, t.TempDir(), env, "build", "--name", "size")
	if r.code != 0 || !strings.Contains(r.out, "made 1, upgraded 0, remade 0, failed 0") {
		t.Fatalf("build ended %d:\n%s%s", r.code, r.out, r.errs)
	}
	if info := servertest.Read(t, strings.TrimSuffix(later, ".mkv")+"-640-10.bif"); info.Frames != 4 || info.Width != 640 || info.Height != 360 || info.Note != "ffmpeg 640" {
		t.Errorf("the BIF built holds %+v, want 4 pictures 640x360 with ebb's note", info)
	}
}
