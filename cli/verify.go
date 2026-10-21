package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
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

func verifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "List the BIFs that cannot be read, or whose pictures stop well before their video ends or run well past it",
		Long: `Reads every BIF beside Emby's videos and lists each that is wrong:

  broken  it cannot be read: it is not a BIF, it was cut short, its index is
          out of order, or its first picture cannot be shown
  short   its pictures stop well before the video does: one picture for an
          hour of video, say
  long    its pictures run well past the video's end, as a BIF of another cut
          of the video would

How long each video is comes from Emby's database; a BIF of a video Emby has no
length for is only checked for whether it can be read. --pictures also reads
every picture in every BIF, which reads the whole of each rather than its start.

Each line says whether copy --upgrade or build --upgrade replaces the BIF. The run
ends badly when any BIF is wrong. Strays, BIFs beside no video, are listed by strays.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			pictures, _ := cmd.Flags().GetBool("pictures")

			return runVerify(cmd, GetFlags(), pictures)
		},
	}

	cmd.Flags().Bool("pictures", false, "also read every picture in every BIF, which reads the whole of each")

	return cmd
}

// runVerify lists the BIFs that are wrong.
func runVerify(cmd *cobra.Command, f *FlagData, pictures bool) error {
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
	lengths, err := s.emby.Lengths(ctx, ids)
	if err != nil {
		return err
	}

	if pictures {
		if err := readPictures(ctx, videos, f.Workers, cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("stopped: %w", err)
		}
	}

	every := map[string]time.Duration{}
	for _, l := range inv.Libraries {
		every[l.Name] = l.Interval
	}

	var (
		bifs, unknown   int
		wrong, unopened int
		kinds           = map[verify.Kind]int{}
		fixes           = map[string]int{}
	)
	for _, v := range videos {
		if lengths[v.ID] <= 0 {
			unknown += len(v.BIFs)
		}

		// what replaces the video's BIFs is only worked out for a video with one that is wrong, as building's answer can be a question of Emby's
		// database
		fix, asked := "", false
		for _, b := range v.BIFs {
			bifs++
			p, isWrong := verify.Check(b, lengths[v.ID])
			if !isWrong {
				cout.Verbosef("%s %s\n", paint(good, "fine     "), b.Path)

				continue
			}
			// nothing is known of a BIF that cannot be opened, so it is said but not counted as wrong
			if p.Kind == verify.Unopened {
				unopened++
				cout.Printf("%s %s: %s\n", paint(todo, fmt.Sprintf("%-9s", p.Kind)), b.Path, p.Why)

				continue
			}

			if !asked {
				fix, asked = s.replacer(ctx, f, naming, every[v.Library], v), true
			}
			wrong++
			kinds[p.Kind]++
			fixes[fix]++

			line := fmt.Sprintf("%s %s: %s", paint(bad, fmt.Sprintf("%-9s", p.Kind)), b.Path, p.Why)
			if fix != "" {
				line += "; " + paint(good, fix+" replaces it")
			}
			cout.Printf("%s\n", line)
		}
	}

	said := []string{"read " + plural(bifs, "BIF", "BIFs") + ": " + paint(good, "none is wrong")}
	if wrong > 0 {
		var counts []string
		for _, k := range []verify.Kind{verify.Broken, verify.Short, verify.Long} {
			if kinds[k] > 0 {
				counts = append(counts, paint(bad, number(kinds[k])+" "+k.String()))
			}
		}
		said = []string{"read " + plural(bifs, "BIF", "BIFs") + ": " + strings.Join(counts, ", ")}
		for _, fix := range []string{"copy --upgrade", "build --upgrade"} {
			if fixes[fix] > 0 {
				said = append(said, fix+" replaces "+amount(fixes[fix], good))
			}
		}
	}
	if unopened > 0 {
		said = append(said, paint(todo, number(unopened)+" could not be opened from here"))
	}
	cout.Quietf("%s\n", strings.Join(said, "; "))
	if unknown > 0 {
		cout.Quietf("%s\n", paint(quiet, fmt.Sprintf("Emby has no length for the videos of %s, so those were only checked for whether they can be read", plural(unknown, "BIF", "BIFs"))))
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stopped: %w", err)
	}
	if wrong > 0 {
		return fmt.Errorf("%s wrong", plural(wrong, "BIF is", "BIFs are"))
	}

	return nil
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
func readPictures(ctx context.Context, videos []*inventory.Video, workers int, progress io.Writer) error {
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
			color.Fprintln(progress, paint(quiet, "read every picture of ")+paint(mark, number(n))+paint(quiet, " of ")+paint(mark, number(len(whole)))+paint(quiet, " BIFs"))
		}
		mu.Unlock()

		return ctx.Err()
	})
}
