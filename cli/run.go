package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gookit/color"
	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/paths"
	"github.com/katbyte/emby-bif-builder/lib/place"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
	"github.com/katbyte/emby-bif-builder/lib/verify"
	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/go-kt/parallel"
	"github.com/spf13/cobra"
)

// sources is what a run reads: the two databases, open read-only, Jellyfin's trickplay folder, and where the videos are found.
type sources struct {
	emby      *emby.DB
	jellyfin  *jellyfin.DB
	trickplay string
	paths     paths.Map
}

// open opens what the settings name. Emby's database is always needed; Jellyfin's, and its trickplay folder, only when jellyfin says so, and are
// otherwise opened if they are set.
func (f *FlagData) open(ctx context.Context, jellyfinNeeded bool) (*sources, error) {
	settings := map[string]string{"emby-db-path": f.EmbyDB}
	if jellyfinNeeded {
		settings["jf-db-path"], settings["jf-trickplay-path"] = f.JellyfinDB, f.Trickplay
	}
	if err := need(settings); err != nil {
		return nil, err
	}

	m, err := f.paths()
	if err != nil {
		return nil, err
	}

	s := &sources{trickplay: home(f.Trickplay), paths: m}
	if s.emby, err = emby.Open(ctx, home(f.EmbyDB)); err != nil {
		return nil, fmt.Errorf("--emby-db-path (%s): %w", envNames["emby-db-path"], err)
	}
	if f.JellyfinDB != "" {
		if s.jellyfin, err = jellyfin.Open(ctx, home(f.JellyfinDB)); err != nil {
			s.close()

			return nil, fmt.Errorf("--jf-db-path (%s): %w", envNames["jf-db-path"], err)
		}
	}

	return s, nil
}

// changed is when Jellyfin last wrote the sheets of a set of thumbnails.
func (s *sources) changed(i trickplay.Info) (time.Time, error) {
	return trickplay.Changed(s.trickplay, i)
}

// close lets the databases go.
func (s *sources) close() {
	_ = s.emby.Close()
	if s.jellyfin != nil {
		_ = s.jellyfin.Close()
	}
}

// take reads what every video has, saying as each step starts and ends, and every few seconds while it reads the folders, unless told to be quiet.
func (f *FlagData) take(ctx context.Context, s *sources, out io.Writer) (*inventory.Inventory, error) {
	var (
		step          inventory.Step
		started, last time.Time
	)
	say := func(parts ...string) { color.Fprintln(out, strings.Join(parts, "")) }
	report := func(e inventory.Event) {
		if cout.Level < cout.VerbosityNormal {
			return
		}
		if e.Step != step {
			step, started, last = e.Step, time.Now(), time.Now()
		}
		took := paint(quiet, ", in "+verify.Length(time.Since(started)))

		switch {
		case e.Step == inventory.EmbyVideos && !e.Ended:
			say(paint(quiet, "reading Emby's videos"))
		case e.Step == inventory.EmbyVideos:
			say(paint(quiet, "Emby has "), paint(mark, number(e.Done)), paint(quiet, " videos in these libraries"), took)
		case e.Step == inventory.JellyfinThumbnails && !e.Ended:
			say(paint(quiet, "reading Jellyfin's thumbnails"))
		case e.Step == inventory.JellyfinThumbnails:
			say(paint(quiet, "Jellyfin has thumbnails of "), paint(mark, number(e.Done)), paint(quiet, " videos"), took)
		case e.Ended:
			say(paint(quiet, "read "), paint(mark, number(e.Total)), paint(quiet, " folders"), took)
		case e.Done == 0:
			say(paint(quiet, "reading the "), paint(mark, number(e.Total)), paint(quiet, " folders the videos are in"))
		case time.Since(last) >= 5*time.Second:
			last = time.Now()
			far := progress(e.Done, e.Total)
			say(paint(quiet, "read "), paint(far, number(e.Done)), paint(quiet, " of "), paint(mark, number(e.Total)), paint(quiet, " folders, "), paint(far, fmt.Sprintf("%d%%", e.Done*100/e.Total)))
		}
	}

	return inventory.Take(ctx, inventory.Options{Emby: s.emby, Jellyfin: s.jellyfin, Paths: s.paths, Libraries: f.libraries(), Workers: f.Workers, Progress: report})
}

// inScope says whether a video is one the commands give BIFs to: films and episodes, and extras when --extras says so.
func (f *FlagData) inScope(v *inventory.Video) bool {
	return v.Kind != emby.Extra || f.Extras
}

// signals is the command's context, ended by an interrupt, so a run stops between BIFs and leaves no half-written one behind.
func signals(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
}

// writer makes a step's BIF: its bytes, and the path to put them at, which a step building from the video only knows once it has the pictures.
type writer func(ctx context.Context, s plan.Step) (data []byte, target string, err error)

// tally is what a run did.
type tally struct {
	mu      sync.Mutex
	done    map[plan.Action]int
	left    map[plan.Reason]int
	removed int
	failed  int
}

// job is how a run makes each video's BIF: write makes it, and from says what it is made from, for the lines that say what is being done.
type job struct {
	write writer
	from  func(plan.Step) string
}

// carryOut does each step that is not a leave, workers at a time, and at most limit of them when limit is above 0. Each is said briefly as it
// starts, then again once done: which of how many is done, what was made, how long it took, how long the rest may take, its folder and what it was
// made from. A step that fails is said and counted, and the rest go on. A dry run says what would be done and does nothing.
func carryOut(ctx context.Context, steps []plan.Step, workers, limit int, dry bool, j job, rec *place.Record) *tally {
	t := &tally{done: map[plan.Action]int{}, left: map[plan.Reason]int{}}

	var doing []plan.Step
	for _, s := range steps {
		if s.Action == plan.Leave {
			t.left[s.Reason]++
			cout.Verbosef("%s %s: %s\n", paint(quiet, "left     "), named(s.Video.Local), paint(quiet, s.Why))

			continue
		}
		if limit > 0 && len(doing) >= limit {
			continue
		}
		doing = append(doing, s)
	}

	// a line that says something done starts with how many of how many are done, and the lines under it line up after that
	of := number(len(doing))
	at := func(n int) string {
		return paint(progress(n, len(doing)), fmt.Sprintf("[%*s/%s]", len(of), number(n), of))
	}
	under := strings.Repeat(" ", len(of)*2+3)
	width := 9
	if dry {
		width = 13
	}
	verb := func(colour, word string) string { return paint(colour, fmt.Sprintf("%-*s", width, word)) }
	detail := func(label, text string) string {
		return fmt.Sprintf("%s %s %s\n", under, paint(quiet, fmt.Sprintf("%-*s", width, label)), text)
	}
	where := func(s plan.Step, target string) string {
		return detail("in", paint(quiet, filepath.Dir(target))) + detail("from", j.from(s))
	}

	if dry {
		for i, s := range doing {
			t.done[s.Action]++
			cout.Printf("%s %s %s: %s\n%s", at(i+1), verb(todo, "would "+s.Action.String()), paint("bold", filepath.Base(s.Target)), paint(quiet, s.Why), where(s, s.Target))
		}

		return t
	}

	var (
		ended int
		begun = time.Now()
	)
	_ = parallel.Each(ctx, len(doing), workers, func(ctx context.Context, i int) error {
		s := doing[i]
		t.mu.Lock()
		cout.Printf("%s %s %s: %s\n", under, verb(todo, underway(s.Action)), paint("bold", filepath.Base(s.Target)), paint(quiet, s.Why))
		t.mu.Unlock()
		start := time.Now()

		data, target, err := j.write(ctx, s)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			t.mu.Lock()
			defer t.mu.Unlock()

			ended++
			t.failed++
			cout.Errorf("%s %s %s: %s\n%s", at(ended), verb(bad, "failed"), paint("bold", filepath.Base(s.Video.Local)), paint(bad, err.Error()), where(s, s.Target))

			return nil
		}

		replace := s.Replace
		if target != s.Target {
			replace = plan.Others(s.Video, target)
		}
		changes, err := place.Put(target, data, s.Action != plan.Make, replace, rec, s.Why)

		t.mu.Lock()
		defer t.mu.Unlock()

		ended++
		if len(changes) == 0 {
			t.failed++
			cout.Errorf("%s %s %s: %s\n%s", at(ended), verb(bad, "failed"), paint("bold", filepath.Base(target)), paint(bad, err.Error()), where(s, target))

			return nil
		}

		left := time.Duration(float64(time.Since(begun)) / float64(ended) * float64(len(doing)-ended))
		said := ""
		for _, c := range changes {
			if c.What == "removed" {
				t.removed++
				said += detail("", verb(todo, "removed")+" "+filepath.Base(c.Path))

				continue
			}
			t.done[s.Action]++
			said = fmt.Sprintf("%s %s %s: %s, in %s%s\n", at(ended), verb(good, past(s.Action)), paint("bold", filepath.Base(c.Path)), holds(data), verify.Length(time.Since(start)), paint(quiet, remaining(ended, len(doing), left))) + where(s, c.Path) + said
		}
		cout.Quietf("%s", said)
		// the BIF went in, and what then failed was removing one it replaces
		if err != nil {
			t.failed++
			cout.Errorf("%s %s %s: %s\n", under, verb(bad, "failed"), paint("bold", filepath.Base(target)), paint(bad, err.Error()))
		}

		return nil
	})

	return t
}

// holds is what a BIF just made holds, as a line saying it was made puts it: "520 pictures, 640x360, 4.1 MB".
func holds(data []byte) string {
	info, err := bif.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return size(int64(len(data)))
	}

	return fmt.Sprintf("%s, %dx%d, %s", plural(info.Frames, "picture", "pictures"), info.Width, info.Height, size(int64(len(data))))
}

// remaining is how long the rest of a run may take, going by how long the ones done so far took, once there are enough done to tell.
func remaining(done, of int, left time.Duration) string {
	if done < 3 || done == of {
		return ""
	}

	return "; about " + verify.Length(left) + " left"
}

// underway is the action as it is under way, as the line that starts it says.
func underway(a plan.Action) string {
	switch a {
	case plan.Make:
		return "making"
	case plan.Upgrade:
		return "upgrading"
	case plan.Remake:
		return "remaking"
	default:
		return a.String()
	}
}

// past is what was done, as a line saying so starts.
func past(a plan.Action) string {
	switch a {
	case plan.Make:
		return "made"
	case plan.Upgrade:
		return "upgraded"
	case plan.Remake:
		return "remade"
	default:
		return a.String()
	}
}

// say writes the totals: what was done, or would be, what was left and why, what was removed, what failed, and where the record is.
func (t *tally) say(dry bool, rec *place.Record) {
	var done []string
	for _, a := range []plan.Action{plan.Make, plan.Upgrade, plan.Remake} {
		if dry {
			done = append(done, fmt.Sprintf("would %s %s", a, amount(t.done[a], todo)))
		} else {
			done = append(done, fmt.Sprintf("%s %s", past(a), amount(t.done[a], good)))
		}
	}
	if !dry {
		done = append(done, "failed "+amount(t.failed, bad))
	}
	cout.Quietf("%s\n", strings.Join(done, ", "))

	var left []string
	for _, r := range []plan.Reason{plan.Done, plan.Held, plan.Elsewhere, plan.Gone, plan.Unreachable, plan.Unsure} {
		if n := t.left[r]; n > 0 {
			left = append(left, fmt.Sprintf("%s %s", amount(n, leftColour[r]), r))
		}
	}
	if len(left) > 0 {
		cout.Printf("left as they are: %s\n", strings.Join(left, ", "))
	}

	if !dry && (t.removed > 0 || t.done[plan.Make]+t.done[plan.Upgrade]+t.done[plan.Remake] > 0) {
		line := fmt.Sprintf("removed %s old BIFs", amount(t.removed, todo))
		if rec != nil {
			line += paint(quiet, "; every change is recorded in "+rec.Path)
		}
		cout.Quietf("%s\n", line)
	}
}

// leftColour is the colour of each count of videos left as they are: those that have what they need are good, those --upgrade or another run would
// see to are still to do, and those not there are bad.
var leftColour = map[plan.Reason]string{plan.Done: good, plan.Held: todo, plan.Elsewhere: quiet, plan.Gone: bad, plan.Unreachable: todo, plan.Unsure: todo}

// err is the run's error: none unless a step failed.
func (t *tally) err() error {
	if t.failed > 0 {
		return fmt.Errorf("%s failed", plural(t.failed, "video", "videos"))
	}

	return nil
}

// record opens the record of changes the settings name, for a run that changes anything.
func (f *FlagData) record(dry bool) (*place.Record, error) {
	if dry || f.Record == "" {
		return nil, nil
	}

	path := home(f.Record)
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}

	rec, err := place.OpenRecord(path)
	if err != nil {
		return nil, fmt.Errorf("--record (%s): %w", envNames["record"], err)
	}

	return rec, nil
}

// number is n with commas between its thousands, as a person writes it.
func number(n int) string {
	if n < 0 {
		return "-" + number(-n)
	}
	s := strconv.Itoa(n)

	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}

	return b.String()
}

// plural is n and the word for one of them or for many.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return number(n) + " " + many
}
