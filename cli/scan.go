package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gookit/color"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/verify"
	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/go-kt/parallel"
	"github.com/spf13/cobra"
)

func scanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Count what has a BIF, and list each BIF that is wrong or stray and each video not found",
		Long: `Reads Emby's and Jellyfin's databases and every folder Emby's videos are in, once, and says
what it found. First each thing to look at, a line each:

  broken     a BIF that cannot be read: not a BIF, cut short, or its index out of order
  short      a BIF whose pictures stop well before its video does: one picture for an
             hour of video, say
  long       a BIF whose pictures run well past its video's end, as one of another cut
             of the video would
  unopened   a BIF that is listed but cannot be opened from here
  stray      a BIF beside no video of its name, or one a stopped run of ebb half wrote
  not found  a video Emby has whose file is not there

Then the counts, library by library: films, shows, episodes and extras, how many have a
BIF, how many Jellyfin has thumbnails of, and what copy would make or replace. Then the
BIFs: how wide, how named, who made them and how many are wrong. Then Jellyfin's
thumbnails.

How long each video is comes from Emby's database. --pictures also reads every picture in
every BIF, which reads the whole of each rather than its start. --quiet gives the counts
alone. Extras are counted, and left out of the rest unless --extras is given.

Nothing is changed: clean removes the strays, and copy --upgrade or build --upgrade
replaces a wrong BIF, as its line says.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			pictures, _ := cmd.Flags().GetBool("pictures")

			return runScan(cmd, GetFlags(), pictures)
		},
	}

	cmd.Flags().Bool("pictures", false, "also read every picture in every BIF, which reads the whole of each")

	return cmd
}

// findings is what a scan found beyond what the inventory holds, for the counts after the lists.
type findings struct {
	// lengths is how long Emby has each video with a BIF as, by ID.
	lengths map[int64]time.Duration
	// fixes is how many of the wrong BIFs each run would replace: copy --upgrade or build --upgrade.
	fixes map[string]int
}

// runScan reads everything once, lists what is wrong, then gives the counts.
func runScan(cmd *cobra.Command, f *FlagData, pictures bool) error {
	if err := f.check(); err != nil {
		return err
	}
	naming, err := f.naming()
	if err != nil {
		return err
	}

	ctx, stop := signals(cmd)
	defer stop()

	s, err := f.open(ctx, false)
	if err != nil {
		return err
	}
	defer s.close()

	inv, err := f.take(ctx, s, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	var (
		videos []*inventory.Video
		ids    []int64
	)
	for _, v := range inv.Videos {
		if f.inScope(v) && len(v.BIFs) > 0 {
			videos = append(videos, v)
			ids = append(ids, v.ID)
		}
	}
	found := findings{fixes: map[string]int{}}
	if found.lengths, err = s.emby.Lengths(ctx, ids); err != nil {
		return err
	}

	if pictures {
		if err := readPictures(ctx, videos, f.Workers, cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("stopped: %w", err)
		}
	}

	s.listWrong(ctx, f, naming, inv, videos, found)
	listStrays(inv)
	listNotFound(f, inv)
	writeStats(cmd.OutOrStdout(), f, s, inv, found)

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stopped: %w", err)
	}

	return nil
}

// listWrong says each BIF that is wrong, with the run that would replace it, and each that cannot be opened from here.
func (s *sources) listWrong(ctx context.Context, f *FlagData, naming plan.Naming, inv *inventory.Inventory, videos []*inventory.Video, found findings) {
	every := map[string]time.Duration{}
	for _, l := range inv.Libraries {
		every[l.Name] = l.Interval
	}

	for _, v := range videos {
		// what replaces the video's BIFs is only worked out for a video with one that is wrong, as building's answer can be a question of Emby's
		// database
		fix, asked := "", false
		for _, b := range v.BIFs {
			p, isWrong := verify.Check(b, found.lengths[v.ID])
			switch {
			case !isWrong:
				cout.Verbosef("%s %s\n", paint(good, "fine     "), named(b.Path))

				continue
			// nothing is known of a BIF that cannot be opened, so nothing replaces it from here
			case p.Kind == verify.Unopened:
				cout.Printf("%s %s: %s\n", paint(todo, fmt.Sprintf("%-9s", p.Kind)), named(b.Path), p.Why)

				continue
			}

			if !asked {
				fix, asked = s.replacer(ctx, f, naming, every[v.Library], v), true
			}
			found.fixes[fix]++

			line := fmt.Sprintf("%s %s: %s", paint(bad, fmt.Sprintf("%-9s", p.Kind)), named(b.Path), p.Why)
			if fix != "" {
				line += "; " + paint(good, fix+" replaces it")
			}
			cout.Printf("%s\n", line)
		}
	}
}

// listStrays says each stray: a BIF beside no video of its name, or one a stopped run half wrote.
func listStrays(inv *inventory.Inventory) {
	for _, st := range inv.Strays {
		cout.Printf("%s %s: %s, %s\n", paint(todo, "stray    "), named(st.Path), st.Why, size(st.Bytes))
	}
}

// listNotFound says each video Emby has whose file is not there, and why.
func listNotFound(f *FlagData, inv *inventory.Inventory) {
	for _, v := range inv.Videos {
		if f.inScope(v) && (v.Err != nil || !v.Found) {
			cout.Printf("%s %s: %s; %s\n", paint(bad, "not found"), named(v.Local), whyNotFound(v), paint(quiet, fmt.Sprintf("Emby has it as %s %d in %s", v.Kind, v.ID, v.Library)))
		}
	}
}

// whyNotFound says why a video Emby has is not there: its folder is there and it is not, or the folder is gone too or cannot be read.
func whyNotFound(v *inventory.Video) string {
	switch {
	case errors.Is(v.Err, fs.ErrNotExist):
		return "its folder is gone too"
	case v.Err != nil:
		return "its folder cannot be read: " + v.Err.Error()
	default:
		return "its folder is there, the file is not"
	}
}

// replacer is the run that would replace the video's BIFs, "copy --upgrade" or "build --upgrade", or "" when neither would.
func (s *sources) replacer(ctx context.Context, f *FlagData, naming plan.Naming, interval time.Duration, v *inventory.Video) string {
	if v.Jellyfin != nil {
		switch plan.Copy(v, plan.CopyOptions{Naming: naming, Upgrade: true, Changed: s.changed}).Action {
		case plan.Upgrade, plan.Remake:
			return "copy --upgrade"
		case plan.Leave, plan.Make:
		}
	}

	if interval <= 0 {
		interval = defaultInterval
	}
	if plan.Build(v, plan.BuildOptions{Naming: naming, Upgrade: true, Width: f.Size, Interval: interval, VideoWidth: videoWidth(ctx, s.emby)}).Action == plan.Upgrade {
		return "build --upgrade"
	}

	return ""
}

// readPictures reads every picture of each BIF that can be read, a few at once, marking each with one that cannot be shown as not readable. A line
// on the way says how far it has got.
func readPictures(ctx context.Context, videos []*inventory.Video, workers int, out io.Writer) error {
	var whole []*inventory.BIF
	for _, v := range videos {
		for i := range v.BIFs {
			if v.BIFs[i].Err == nil {
				whole = append(whole, &v.BIFs[i])
			}
		}
	}

	var (
		done atomic.Int64
		mu   sync.Mutex
		last time.Time
	)

	return parallel.Each(ctx, len(whole), max(workers, 1), func(ctx context.Context, i int) error {
		inventory.ReadPictures(whole[i])

		n := int(done.Add(1))
		mu.Lock()
		if cout.Level >= cout.VerbosityNormal && (time.Since(last) >= 5*time.Second || n == len(whole)) {
			last = time.Now()
			far := progress(n, len(whole))
			color.Fprintln(out, paint(quiet, "read every picture of ")+paint(far, number(n))+paint(quiet, " of ")+paint(mark, number(len(whole)))+paint(quiet, " BIFs, ")+paint(far, fmt.Sprintf("%d%%", n*100/len(whole))))
		}
		mu.Unlock()

		return ctx.Err()
	})
}
