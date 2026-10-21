package cli

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gookit/color"
	"github.com/katbyte/emby-bif-builder/lib/servertest"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/viper"
)

// alone cuts a test off from the settings of whoever runs it: viper and the output as they start, an empty home and working directory, and none of
// the program's variables set. viper is global, so none of these tests can run in parallel.
func alone(t *testing.T) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	level, out, errs := cout.Level, cout.Out, cout.Err
	t.Cleanup(func() { cout.Level, cout.Out, cout.Err = level, out, errs }) //nolint:reassign // each command points cout at its own output, and this puts it back
	// what is printed is read as text, without the colours a terminal would show
	colours := color.Enable
	color.Enable = false
	t.Cleanup(func() { color.Enable = colours })

	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv(configEnv, "")
	for _, env := range envNames {
		t.Setenv(env, "")
	}
}

// set points the program at the made-up servers, and keeps the record of changes in the test's own folder.
func set(t *testing.T, s *servertest.Servers) string {
	t.Helper()

	s.Done()
	record := filepath.Join(t.TempDir(), "changes.tsv")
	for name, value := range map[string]string{"emby-db-path": s.EmbyDB, "jf-db-path": s.JellyfinDB, "jf-trickplay-path": s.Trickplay, "media-paths": s.MediaPaths(), "record": record} {
		t.Setenv(envNames[name], value)
	}

	return record
}

// run executes the program with these arguments and gives back what it printed, and what it said went wrong.
func run(t *testing.T, args ...string) (printed, said string, err error) {
	t.Helper()

	root, err := Make()
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	var out, errs bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetArgs(args)
	err = root.ExecuteContext(t.Context())

	return out.String(), errs.String(), err
}

// library is a TV library and a films one, each video in a state copy, upgrade or build has to tell apart.
type library struct {
	*servertest.Servers
	// fresh has Jellyfin thumbnails and no BIF; narrow has Emby's own 320-wide BIF; twice has that and an old bare one too; built has no
	// thumbnails and no BIF; wide has a 640-wide BIF Emby did not make; gone is a video Emby has whose file is not there.
	fresh, narrow, twice, built, wide, gone string
	// film has 544-wide thumbnails, and trailer, an extra, thumbnails and no BIF.
	film, trailer string
	// freshThumbnails is Jellyfin's set for fresh.
	freshThumbnails trickplay.Info
}

func newLibrary(t *testing.T) *library {
	t.Helper()

	s := servertest.New(t)
	l := &library{Servers: s}
	s.Library("TV", "tvshows", "tv")
	s.Library("Movies", "movies", "movies")
	s.Show("tv/Show (2020)")

	l.fresh = s.Episode("tv/Show (2020)/Season 1/Show - 01x01 - Fresh.mkv")
	l.freshThumbnails = s.Thumbnails(l.fresh, 64, 36, 23)

	l.narrow = s.Episode("tv/Show (2020)/Season 1/Show - 01x02 - Narrow.mkv")
	s.Thumbnails(l.narrow, 64, 36, 23)
	s.BIF(l.narrow, "Show - 01x02 - Narrow-320-10.bif", 32, 18, 23)

	l.twice = s.Episode("tv/Show (2020)/Season 1/Show - 01x03 - Twice.mkv")
	s.Thumbnails(l.twice, 64, 36, 23)
	s.BIF(l.twice, "Show - 01x03 - Twice-320-10.bif", 32, 18, 23)
	s.BIF(l.twice, "Show - 01x03 - Twice.bif", 32, 18, 23)

	l.built = s.Episode("tv/Show (2020)/Season 1/Show - 01x04 - Built.mkv")

	l.wide = s.Episode("tv/Show (2020)/Season 1/Show - 01x05 - Wide.mkv")
	s.Thumbnails(l.wide, 64, 36, 23)
	s.BIF(l.wide, "Show - 01x05 - Wide-640-10.bif", 64, 36, 23)

	l.gone = s.Episode("tv/Show (2020)/Season 1/Show - 01x06 - Gone.mkv")
	if err := os.Remove(s.Local(l.gone)); err != nil {
		t.Fatal(err)
	}

	l.film = s.Film("movies/Film (2000)/Film (2000).mkv")
	s.Thumbnails(l.film, 54, 40, 5)
	l.trailer = s.Trailer("movies/Film (2000)/trailers/Film (2000)-trailer.mkv")
	s.Thumbnails(l.trailer, 64, 36, 3)

	return l
}

// bif is where the test finds the BIF of this name beside the video at path.
func (l *library) bif(path, name string) string {
	return filepath.Join(filepath.Dir(l.Local(path)), name)
}

// files is every file under the media folder, for seeing what a run changed.
func (l *library) files(t *testing.T) []string {
	t.Helper()

	var all []string
	if err := filepath.WalkDir(l.Media, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			all = append(all, strings.TrimPrefix(path, l.Media))
		}

		return err
	}); err != nil {
		t.Fatal(err)
	}

	return all
}

// TestStatsCountsEachLibrary counts films, shows, episodes and extras, what has a BIF, and what copy would do, library by library.
func TestScanCountsEachLibrary(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	out, _, err := run(t, "scan")
	if err != nil {
		t.Fatal(err)
	}

	// library, films, shows, episodes, extras, with a BIF, without, not found, Jellyfin has, to copy, to upgrade
	for _, want := range [][]string{
		{"Movies", "1", "0", "0", "1", "0", "1", "0", "1", "1", "0"},
		{"TV", "0", "1", "6", "0", "3", "2", "1", "4", "1", "2"},
		{"total", "1", "1", "6", "1", "3", "3", "1", "5", "2", "2"},
	} {
		if !hasRow(out, want) {
			t.Errorf("no row %v in:\n%s", want, out)
		}
	}
	for _, want := range []string{"BIFs beside the films and episodes: 4", "by width:  32: 3, 64: 1", "by name:   -320-10: 2, -640-10: 1, bare: 1", "Jellyfin has thumbnails of 5 of these videos", "by width:  64: 4, 54: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the counts lack %q:\n%s", want, out)
		}
	}
}

// hasRow says whether the output has a line of these fields.
func hasRow(out string, want []string) bool {
	for line := range strings.Lines(out) {
		if slices.Equal(strings.Fields(line), want) {
			return true
		}
	}

	return false
}

// TestADryRunChangesNothing says what copy would do, and does none of it.
func TestADryRunChangesNothing(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	record := set(t, l.Servers)
	before := l.files(t)

	out, _, err := run(t, "copy", "--upgrade", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}

	if after := l.files(t); !slices.Equal(before, after) {
		t.Errorf("a dry run changed the files from %v to %v", before, after)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("a dry run started a record of changes")
	}
	// each says which of how many it is, what it would do and why, what its BIF would be cut from and where it would go
	for _, want := range []string{
		"[2/4] would make    Show - 01x01 - Fresh-320-10.bif: no BIF\n      in            " + filepath.Dir(l.Local(l.fresh)) + "\n      from          Jellyfin's 23 thumbnails on 1 sheet in " + l.freshThumbnails.Folder(l.Trickplay) + "\n",
		"would upgrade Show - 01x02 - Narrow-320-10.bif: its BIF is 32 wide, Jellyfin's thumbnails 64\n",
		"would make 2, would upgrade 2, would remake 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, out)
		}
	}
}

// TestCopyCutsJellyfinsThumbnails makes a BIF for each video with thumbnails and none of its own: Jellyfin's thumbnails, in order, as wide as
// Jellyfin's, named as Emby names its own, with ebb's note in it. A video that has one, even a narrower one, is left without --upgrade.
func TestCopyCutsJellyfinsThumbnails(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	record := set(t, l.Servers)

	out, _, err := run(t, "copy")
	if err != nil {
		t.Fatal(err)
	}

	made := l.bif(l.fresh, "Show - 01x01 - Fresh-320-10.bif")
	info := servertest.Read(t, made)
	if info.Frames != 23 || info.Width != 64 || info.Height != 36 || info.Interval != 10*time.Second || !strings.HasPrefix(info.Note, "jellyfin ") {
		t.Errorf("the BIF made holds %+v, want 23 pictures 64x36 ten seconds apart with ebb's note", info)
	}
	for k, pic := range servertest.Frames(t, made) {
		if got, want := int(servertest.ShadeOf(t, pic)), int(servertest.Shade(k)); got < want-6 || got > want+6 {
			t.Errorf("picture %d is shade %d, want about %d: not thumbnail %d", k, got, want, k)
		}
	}

	if info := servertest.Read(t, l.bif(l.film, "Film (2000)-320-10.bif")); info.Width != 54 || info.Frames != 5 {
		t.Errorf("the film's BIF holds %+v, want Jellyfin's 5 pictures 54 wide", info)
	}
	if info := servertest.Read(t, l.bif(l.narrow, "Show - 01x02 - Narrow-320-10.bif")); info.Width != 32 {
		t.Errorf("without --upgrade the narrow BIF was replaced: it is now %d wide", info.Width)
	}
	if _, err := os.Stat(l.bif(l.trailer, "Film (2000)-trailer-320-10.bif")); err == nil {
		t.Error("the trailer was given a BIF without --extras")
	}

	if !strings.Contains(out, "made 2, upgraded 0, remade 0, failed 0") {
		t.Errorf("the totals are wrong:\n%s", out)
	}
	// each is said as it starts, and again once it is made, with how many of how many are done, what it holds, its folder and what it was cut from
	for _, want := range []string{
		" making    Show - 01x01 - Fresh-320-10.bif: no BIF\n",
		"] made      Show - 01x01 - Fresh-320-10.bif: 23 pictures, 64x36, ",
		"      in        " + filepath.Dir(made) + "\n      from      Jellyfin's 23 thumbnails on 1 sheet in " + l.freshThumbnails.Folder(l.Trickplay) + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("copy does not say %q:\n%s", want, out)
		}
	}
	if lines := recorded(t, record); len(lines) != 2 || !strings.Contains(lines[0], "\twrote\t") {
		t.Errorf("the record holds %q, want the two BIFs written", lines)
	}
	leftovers(t, l)

	// a second run finds every one done
	out, _, err = run(t, "copy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "made 0, upgraded 0, remade 0, failed 0") {
		t.Errorf("a second run did more:\n%s", out)
	}
}

// recorded is the record's lines.
func recorded(t *testing.T, path string) []string {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}

	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// leftovers fails the test if a hidden file a BIF was written to was left behind.
func leftovers(t *testing.T, l *library) {
	t.Helper()

	for _, f := range l.files(t) {
		if strings.Contains(f, ".ebb-tmp") {
			t.Errorf("%s was left behind", f)
		}
	}
}

// TestUpgradeReplacesNarrowerBIFs replaces Emby's 320-wide BIFs with Jellyfin's wider thumbnails, and removes any other BIF beside the video, so
// Emby has one to choose. What was removed is in the record.
func TestUpgradeReplacesNarrowerBIFs(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	record := set(t, l.Servers)

	out, _, err := run(t, "copy", "--upgrade")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{l.narrow, l.twice} {
		name := strings.TrimSuffix(filepath.Base(v), ".mkv") + "-320-10.bif"
		if info := servertest.Read(t, l.bif(v, name)); info.Width != 64 || !strings.HasPrefix(info.Note, "jellyfin ") {
			t.Errorf("%s holds %+v, want Jellyfin's 64-wide thumbnails", name, info)
		}
	}
	bare := l.bif(l.twice, "Show - 01x03 - Twice.bif")
	if _, err := os.Stat(bare); err == nil {
		t.Error("the old bare BIF beside the upgraded one is still there")
	}
	if info := servertest.Read(t, l.bif(l.wide, "Show - 01x05 - Wide-640-10.bif")); info.Note != "" {
		t.Error("a BIF as wide as Jellyfin's was replaced")
	}

	if !strings.Contains(out, "made 2, upgraded 2, remade 0, failed 0") || !strings.Contains(out, "removed 1 old BIFs") {
		t.Errorf("the totals are wrong:\n%s", out)
	}
	if lines := recorded(t, record); !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, "\tremoved\t"+bare+"\t") }) {
		t.Errorf("the record does not hold the bare BIF's removal: %q", lines)
	}
	leftovers(t, l)
}

// TestRemakeWhenJellyfinMadeItsThumbnailsAgain cuts a BIF ebb made again once Jellyfin has made the thumbnails it was cut from again, and no other.
func TestRemakeWhenJellyfinMadeItsThumbnailsAgain(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	if _, _, err := run(t, "copy"); err != nil {
		t.Fatal(err)
	}

	// Jellyfin writes the fresh episode's sheets again
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(l.freshThumbnails.Folder(l.Trickplay), "0.jpg"), later, later); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, "copy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "made 0, upgraded 0, remade 1, failed 0") || !strings.Contains(out, "] remade    Show - 01x01 - Fresh-320-10.bif: ") {
		t.Errorf("the BIF was not cut again:\n%s", out)
	}
}

// TestLimitStopsAfterSoMany makes no more BIFs than --limit says.
func TestLimitStopsAfterSoMany(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	out, _, err := run(t, "copy", "--upgrade", "--limit", "1", "--workers", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "made 1, upgraded 0, remade 0, failed 0") {
		t.Errorf("--limit 1 did more than one:\n%s", out)
	}
}

// TestNamingTheBIFs names what is written by its width, or bare, when --name says so.
func TestNamingTheBIFs(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	if _, _, err := run(t, "copy", "--name", "size"); err != nil {
		t.Fatal(err)
	}
	servertest.Read(t, l.bif(l.fresh, "Show - 01x01 - Fresh-64-10.bif"))
	servertest.Read(t, l.bif(l.film, "Film (2000)-54-10.bif"))

	if _, _, err := run(t, "copy", "--name", "tiny"); err == nil || !strings.Contains(err.Error(), "--name (EMBY_BIF_BUILDER_NAME)") {
		t.Errorf("a name ebb does not know gave %v, want it said which setting", err)
	}
}

// TestExtrasGetBIFsWhenAskedFor gives the trailer a BIF too with --extras.
func TestExtrasGetBIFsWhenAskedFor(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	if _, _, err := run(t, "copy", "--extras", "--libraries", "movies"); err != nil {
		t.Fatal(err)
	}
	servertest.Read(t, l.bif(l.trailer, "Film (2000)-trailer-320-10.bif"))
	if _, err := os.Stat(l.bif(l.fresh, "Show - 01x01 - Fresh-320-10.bif")); err == nil {
		t.Error("--libraries movies gave the TV library BIFs too")
	}

	if _, _, err := run(t, "copy", "--libraries", "Cartoons"); err == nil || !strings.Contains(err.Error(), "it has Movies, TV") {
		t.Errorf("a library Emby does not have gave %v, want the ones it has listed", err)
	}
}

// TestWhatEachCommandNeeds says which settings are missing, by flag and variable.
func TestWhatEachCommandNeeds(t *testing.T) {
	alone(t)

	_, _, err := run(t, "copy")
	if err == nil || !strings.Contains(err.Error(), "--emby-db-path (EMBY_BIF_BUILDER_EMBY_DB_PATH)") || !strings.Contains(err.Error(), "--jf-trickplay-path (EMBY_BIF_BUILDER_JF_TRICKPLAY_PATH)") {
		t.Errorf("copy with nothing set gave %v, want the settings it needs named", err)
	}

	_, _, err = run(t, "scan", "--workers", "0")
	if err == nil || !strings.Contains(err.Error(), "--workers") {
		t.Errorf("no workers gave %v", err)
	}
}

// TestLookup says what a video has, found by its path, by a BIF beside it, or by Jellyfin's ID for it.
func TestLookup(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	out, _, err := run(t, "lookup", l.narrow, l.bif(l.twice, "Show - 01x03 - Twice.bif"), strings.ToLower(l.freshThumbnails.ItemID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		l.narrow + "\n",
		"Emby:       episode",
		"BIF:        Show - 01x02 - Narrow-320-10.bif",
		"32x18, 23 pictures 10s apart",
		"thumbnails: 64x36, 23 of them 10s apart, on sheets of 10x10",
		"copy would leave: its BIF is 32 wide, Jellyfin's thumbnails 64: --upgrade replaces it",
		"copy --upgrade would upgrade",
		l.twice + "\n",
		"BIF:        Show - 01x03 - Twice.bif",
		l.fresh + "\n",
		"copy would make: no BIF",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lookup does not say %q:\n%s", want, out)
		}
	}

	out, _, err = run(t, "lookup", l.Local(l.fresh))
	if err != nil || !strings.HasPrefix(out, l.fresh+"\n") {
		t.Errorf("looking up the video where it is found here gave %v:\n%s", err, out)
	}

	out, _, err = run(t, "lookup", "no-such-thing.mkv")
	if err == nil || !strings.Contains(out, "nothing has it") {
		t.Errorf("looking up nothing gave %v:\n%s", err, out)
	}
}

// TestStrays lists the BIFs whose video is gone, and removes them with --remove, recording each, while a BIF whose video is there stays.
func TestScanAndCleanStrays(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	record := set(t, l.Servers)
	stray := l.BIF(l.gone, "Show - 01x06 - Gone-320-10.bif", 32, 18, 23)

	out, _, err := run(t, "scan")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stray     "+stray+": no video of its name beside it") || !strings.Contains(out, "  strays:    1, ") {
		t.Errorf("scan does not list and count the gone video's BIF:\n%s", out)
	}

	out, _, err = run(t, "clean", "--dry-run")
	if err != nil || !strings.Contains(out, "would remove "+stray+": ") || !strings.Contains(out, "would remove 1 stray, ") {
		t.Errorf("clean --dry-run does not say it would remove the stray (%v):\n%s", err, out)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Error("scanning, or a dry run of clean, removed a stray")
	}

	out, _, err = run(t, "clean")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("the stray is still there")
	}
	if !strings.Contains(out, "removed 1 stray, ") {
		t.Errorf("the totals are wrong:\n%s", out)
	}
	if lines := recorded(t, record); len(lines) != 1 || !strings.Contains(lines[0], "\tremoved\t"+stray+"\tstray: no video of its name beside it") {
		t.Errorf("the record holds %q", lines)
	}
	servertest.Read(t, l.bif(l.narrow, "Show - 01x02 - Narrow-320-10.bif"))
}

// TestVersion prints the program's name and version.
// scan lists each BIF that cannot be read, or whose pictures stop well before its video ends or run well past it, with the run that replaces it,
// counts them, and changes nothing. Reading every picture finds a picture past the first that cannot be shown.
func TestScanFindsWrongBIFs(t *testing.T) {
	alone(t)
	l := newLibrary(t)

	// narrow's BIF is cut short, built's holds one picture of nearly four minutes, and wide's runs well past its video's minute. twice's videos have
	// no length, so its BIFs are only checked for whether they can be read
	narrow := l.bif(l.narrow, "Show - 01x02 - Narrow-320-10.bif")
	fi, err := os.Stat(narrow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(narrow, fi.Size()-100); err != nil {
		t.Fatal(err)
	}
	built := l.BIF(l.built, "Show - 01x04 - Built-320-10.bif", 32, 18, 1)
	wide := l.bif(l.wide, "Show - 01x05 - Wide-640-10.bif")
	l.Length(l.narrow, 230*time.Second)
	l.Length(l.built, 230*time.Second)
	l.Length(l.wide, time.Minute)
	set(t, l.Servers)
	before := l.files(t)

	out, _, err := run(t, "scan")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"broken    " + narrow + ": cut short: its pictures run to byte ",
		"short     " + built + ": its 1 picture covers 10s, where Emby has the video as 3m 50s long; build --upgrade replaces it\n",
		"long      " + wide + ": its 23 pictures cover 3m 50s, where Emby has the video as 1m 00s long\n",
		"  wrong:     1 broken, 1 short, 1 long; copy --upgrade replaces 1; build --upgrade replaces 1\n",
		"             1 hold a single picture, 1 of them short for their video\n",
		"Emby has no length for the videos of 2 BIFs, so those were only checked for whether they can be read\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("scan does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "; copy --upgrade replaces it\n") {
		t.Errorf("scan does not say copy --upgrade replaces the broken BIF:\n%s", out)
	}

	// the third picture of twice's bare BIF is spoilt where it starts, which reading the start of the file does not reach
	bare := l.bif(l.twice, "Show - 01x03 - Twice.bif")
	raw, err := os.ReadFile(bare) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	at := binary.LittleEndian.Uint32(raw[64+2*8+4:])
	copy(raw[at:], []byte{0, 0, 0, 0})
	if err := os.WriteFile(bare, raw, 0o600); err != nil { //nolint:gosec // the test's own file
		t.Fatal(err)
	}
	if out, _, _ = run(t, "scan"); strings.Contains(out, bare) {
		t.Errorf("scan without --pictures read past the first picture:\n%s", out)
	}

	out, _, err = run(t, "scan", "--pictures")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "broken    "+bare+": broken: picture 2 cannot be shown: ") || !strings.Contains(out, "  wrong:     2 broken, 1 short, 1 long") {
		t.Errorf("scan --pictures does not find the spoilt picture:\n%s", out)
	}

	// --quiet keeps the counts and leaves out the lists
	out, _, err = run(t, "scan", "--quiet")
	if err != nil || strings.Contains(out, narrow) || !strings.Contains(out, "  wrong:     1 broken, 1 short, 1 long") {
		t.Errorf("scan --quiet gave (%v):\n%s", err, out)
	}

	if after := l.files(t); !slices.Equal(before, after) {
		t.Errorf("scan changed the files: before %q, after %q", before, after)
	}
}

// scan lists each video Emby has whose file is not there, and whether its folder is there or gone too.
func TestScanListsVideosNotFound(t *testing.T) {
	alone(t)
	l := newLibrary(t)
	moved := l.Episode("tv/Moved (2001)/Moved - 01x01.mkv")
	if err := os.RemoveAll(filepath.Dir(l.Local(moved))); err != nil {
		t.Fatal(err)
	}
	set(t, l.Servers)

	out, _, err := run(t, "scan")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"not found " + l.Local(l.gone) + ": its folder is there, the file is not; Emby has it as episode ",
		"not found " + l.Local(moved) + ": its folder is gone too; Emby has it as episode ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("scan does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not found "+l.Local(l.fresh)) {
		t.Errorf("scan lists a video that is there as not found:\n%s", out)
	}
}

func TestVersion(t *testing.T) {
	alone(t)

	out, _, err := run(t, "version")
	if err != nil || !strings.HasPrefix(out, "ebb ") {
		t.Errorf("version said %q (%v)", out, err)
	}
}

// TestBuildFromTheVideo builds a BIF from a video Jellyfin has no thumbnails of, with ffmpeg, and leaves the videos Jellyfin has to copy. A test
// without ffmpeg is skipped.
func TestBuildFromTheVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	alone(t)
	l := newLibrary(t)
	set(t, l.Servers)

	// a 25-second video, in place of the stand-in for one
	video := l.Local(l.built)
	if err := os.Remove(video); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "matroska", video).CombinedOutput() //nolint:gosec // the test's own video, in its own folder
	if err != nil {
		t.Fatalf("making the video: %v: %s", err, out)
	}

	said, _, err := run(t, "build", "--name", "size", "--keyframes")
	if err != nil {
		t.Fatal(err)
	}

	info := servertest.Read(t, l.bif(l.built, "Show - 01x04 - Built-640-10.bif"))
	if info.Frames != 3 || info.Width != 640 || info.Height != 360 || info.Note != "ffmpeg 640 keyframes" {
		t.Errorf("the BIF built holds %+v, want 3 pictures 640x360 with ebb's note", info)
	}
	if _, err := os.Stat(l.bif(l.fresh, "Show - 01x01 - Fresh-640-10.bif")); err == nil {
		t.Error("a video Jellyfin has thumbnails of was built from")
	}
	if !strings.Contains(said, "made 1, upgraded 0, remade 0, failed 0") || !strings.Contains(said, "have Jellyfin thumbnails, for copy") {
		t.Errorf("the totals are wrong:\n%s", said)
	}

	// a video ffmpeg cannot read is said, and fails the run, and the others go on
	alone(t)
	set(t, l.Servers)
	if err := os.Remove(l.bif(l.built, "Show - 01x04 - Built-640-10.bif")); err != nil {
		t.Fatal(err)
	}
	l.Episode("tv/Show (2020)/Season 1/Show - 01x07 - Broken.mkv")
	set(t, l.Servers)
	said, errs, err := run(t, "build")
	if err == nil || !strings.Contains(errs, "Show - 01x07 - Broken.mkv: ffmpeg failed") || !strings.Contains(said, "made 1, upgraded 0, remade 0, failed 1") {
		t.Errorf("a video ffmpeg cannot read gave %v:\n%s\n%s", err, said, errs)
	}
}

// A tally names the most common first, and counts the rest together when there are too many to name.
func TestTallies(t *testing.T) {
	t.Parallel()

	if got := tallyOf(map[string]int{"640": 5, "320": 9, "544": 2, "292": 1}, 2, "widths", nil); got != "320: 9, 640: 5, 2 other widths: 3" {
		t.Errorf("tally %q", got)
	}
	if got := tallyOf(nil, 2, "widths", nil); got != "none" {
		t.Errorf("an empty tally reads %q", got)
	}
	if got := number(-1234567); got != "-1,234,567" {
		t.Errorf("a number reads %q", got)
	}
}
