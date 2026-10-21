package cli

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/ffmpeg"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/verify"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
)

// The defaults for building: a picture every ten seconds, as Emby's libraries are set to take them and Jellyfin takes them, and kept about as well
// as Jellyfin keeps its own.
const (
	defaultInterval = 10 * time.Second
	ffmpegQuality   = 3
)

func buildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build BIFs from the videos themselves with ffmpeg, for videos Jellyfin has no thumbnails of",
		Long: `Builds a BIF beside each video that has none and that Jellyfin has no thumbnails of, from
the video itself: ffmpeg decodes it and takes a picture every 10 seconds (or as the video's
library is set), --size wide (640) unless the video is narrower.

Decoding is slow: a film takes a minute or more on the processor. --keyframes takes the
video's key frames alone, which is many times faster, each picture then being the key frame
at or before its time. --hwaccel cuda decodes on an NVIDIA card instead, which the media
servers may be using already. --build-workers says how many videos to decode at once (1).

Videos Jellyfin has thumbnails of are left to copy, which needs no decoding. Without
--jf-db-path, every video without a BIF is built from.

A video that already has a BIF is left as it is, unless --upgrade is given: then one narrower
than --size, and than the video itself, or one that cannot be read, is replaced, and the
BIFs it replaces are removed. BIFs are written as copy writes them, and recorded the same way.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			upgrade, _ := cmd.Flags().GetBool("upgrade")
			dry, _ := cmd.Flags().GetBool("dry-run")
			limit, _ := cmd.Flags().GetInt("limit")

			return runBuild(cmd, GetFlags(), upgrade, dry, limit)
		},
	}

	runFlags(cmd, "replace BIFs narrower than --size and than the video, or that cannot be read, and remove the ones replaced")

	return cmd
}

// runBuild builds BIFs from the videos themselves.
func runBuild(cmd *cobra.Command, f *FlagData, upgrade, dry bool, limit int) error {
	if err := f.check(); err != nil {
		return err
	}
	naming, err := f.naming()
	if err != nil {
		return err
	}

	ctx, stop := signals(cmd)
	defer stop()

	if !dry {
		if err := ffmpeg.Check(ctx, f.FFmpeg); err != nil {
			return fmt.Errorf("--ffmpeg (%s): %w", envNames["ffmpeg"], err)
		}
	}

	s, err := f.open(ctx, false)
	if err != nil {
		return err
	}
	defer s.close()
	if s.jellyfin == nil {
		cout.Printf("%s\n", paint(todo, fmt.Sprintf("Jellyfin's database is not set (--jf-db-path, %s), so videos Jellyfin has thumbnails of are built from too", envNames["jf-db-path"])))
	}

	inv, err := f.take(ctx, s, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	every := map[string]time.Duration{}
	for _, l := range inv.Libraries {
		every[l.Name] = l.Interval
	}

	var steps []plan.Step
	for _, v := range inv.Videos {
		if !f.inScope(v) {
			continue
		}

		interval := every[v.Library]
		if interval <= 0 {
			interval = defaultInterval
		}
		steps = append(steps, plan.Build(v, plan.BuildOptions{Naming: naming, Upgrade: upgrade, Width: f.Size, Interval: interval, VideoWidth: videoWidth(ctx, s.emby)}))
	}

	rec, err := f.record(dry)
	if err != nil {
		return err
	}
	defer func() { _ = rec.Close() }()

	b := builder{o: ffmpeg.Options{Program: f.FFmpeg, HWAccel: f.HWAccel, KeyFrames: f.KeyFrames, Width: f.Size, Quality: ffmpegQuality}, naming: naming, every: every}
	t := carryOut(ctx, steps, f.BuildWorkers, limit, dry, job{write: b.build, from: b.from}, rec)
	t.say(dry, rec)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stopped: %w", err)
	}

	return t.err()
}

// videoWidth is how wide Emby read each video to be, 0 when it does not say.
func videoWidth(ctx context.Context, db *emby.DB) func(v *inventory.Video) int {
	return func(v *inventory.Video) int {
		w, _, err := db.Size(ctx, v.ID)
		if err != nil {
			return 0
		}

		return w
	}
}

// builder builds BIFs from the videos.
type builder struct {
	o      ffmpeg.Options
	naming plan.Naming
	every  map[string]time.Duration
}

// from is the video a step's BIF is built from and how, as the line that starts it says.
func (b builder) from(step plan.Step) string {
	every := b.every[step.Video.Library]
	if every <= 0 {
		every = defaultInterval
	}

	how := "every frame"
	if b.o.KeyFrames {
		how = "key frames only"
	}
	if b.o.HWAccel != "" {
		how += ", on " + b.o.HWAccel
	}

	return fmt.Sprintf("%s, a picture every %s with ffmpeg, %s", step.Video.Local, verify.Length(every), how)
}

// build makes a step's BIF from the video, with a note in its first picture saying it was built so. A BIF named for its width is named once the
// pictures are taken, as only then is it known: the video may be narrower than --size.
func (b builder) build(ctx context.Context, step plan.Step) (data []byte, target string, err error) {
	o := b.o
	if o.Interval = b.every[step.Video.Library]; o.Interval <= 0 {
		o.Interval = defaultInterval
	}

	frames, err := ffmpeg.Frames(ctx, o, step.Video.Local)
	if err != nil {
		return nil, "", err
	}

	cfg, err := jpeg.DecodeConfig(bytes.NewReader(frames[0]))
	if err != nil {
		return nil, "", fmt.Errorf("ffmpeg's first picture cannot be read: %w", err)
	}
	frames[0] = bif.WithNote(frames[0], plan.BuildNote(cfg.Width, o.KeyFrames))

	var buf bytes.Buffer
	if err := bif.Write(&buf, o.Interval, frames); err != nil {
		return nil, "", err
	}

	target = step.Target
	if b.naming == plan.NameSize {
		target = plan.Name(step.Video, plan.NameSize, cfg.Width, o.Interval)
	}

	return buf.Bytes(), target, nil
}
