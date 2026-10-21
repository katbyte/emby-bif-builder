package cli

import (
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

	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/paths"
	"github.com/katbyte/emby-bif-builder/lib/place"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
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

// take reads what every video has, saying how far it has got every few seconds while it reads the folders, unless told to be quiet.
func (f *FlagData) take(ctx context.Context, s *sources, progress io.Writer) (*inventory.Inventory, error) {
	var last time.Time
	report := func(done, total int) {
		if cout.Level < cout.VerbosityNormal || (time.Since(last) < 5*time.Second && done != total) {
			return
		}
		last = time.Now()
		_, _ = fmt.Fprintf(progress, "read %s of %s folders\n", number(done), number(total))
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

// carryOut does each step that is not a leave, workers at a time, and at most limit of them when limit is above 0, saying what is done as it is.
// A step that fails is said and counted, and the rest go on. A dry run says what would be done and does nothing.
func carryOut(ctx context.Context, steps []plan.Step, workers, limit int, dry bool, write writer, rec *place.Record) *tally {
	t := &tally{done: map[plan.Action]int{}, left: map[plan.Reason]int{}}

	var todo []plan.Step
	for _, s := range steps {
		if s.Action == plan.Leave {
			t.left[s.Reason]++
			cout.Verbosef("<gray>left</>      %s: %s\n", s.Video.Local, s.Why)

			continue
		}
		if limit > 0 && len(todo) >= limit {
			continue
		}
		todo = append(todo, s)
	}

	if dry {
		for _, s := range todo {
			t.done[s.Action]++
			cout.Printf("would %-8s %s: %s\n", s.Action, s.Video.Local, s.Why)
		}

		return t
	}

	_ = parallel.Each(ctx, len(todo), workers, func(ctx context.Context, i int) error {
		s := todo[i]

		data, target, err := write(ctx, s)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.fail(s, err)

			return nil
		}

		replace := s.Replace
		if target != s.Target {
			replace = plan.Others(s.Video, target)
		}
		changes, err := place.Put(target, data, s.Action != plan.Make, replace, rec, s.Why)
		t.mu.Lock()
		for _, c := range changes {
			switch c.What {
			case "removed":
				t.removed++
				cout.Printf("<yellow>removed</>   %s\n", c.Path)
			default:
				t.done[s.Action]++
				cout.Quietf("<green>%-9s</> %s\n", past(s.Action), c.Path)
			}
		}
		t.mu.Unlock()
		if err != nil {
			t.fail(s, err)
		}

		return nil
	})

	return t
}

// fail says a step failed, and counts it.
func (t *tally) fail(s plan.Step, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.failed++
	cout.Errorf("<red>failed</>    %s: %v\n", s.Video.Local, err)
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
			done = append(done, fmt.Sprintf("would %s %s", a, number(t.done[a])))
		} else {
			done = append(done, fmt.Sprintf("%s %s", past(a), number(t.done[a])))
		}
	}
	if !dry {
		done = append(done, "failed "+number(t.failed))
	}
	cout.Quietf("%s\n", strings.Join(done, ", "))

	var left []string
	for _, r := range []plan.Reason{plan.Done, plan.Held, plan.Elsewhere, plan.Gone, plan.Unsure} {
		if n := t.left[r]; n > 0 {
			left = append(left, fmt.Sprintf("%s %s", number(n), r))
		}
	}
	if len(left) > 0 {
		cout.Printf("left as they are: %s\n", strings.Join(left, ", "))
	}

	if !dry && (t.removed > 0 || t.done[plan.Make]+t.done[plan.Upgrade]+t.done[plan.Remake] > 0) {
		line := fmt.Sprintf("removed %s old BIFs", number(t.removed))
		if rec != nil {
			line += "; every change is recorded in " + rec.Path
		}
		cout.Quietf("%s\n", line)
	}
}

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
