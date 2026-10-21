package cli

import (
	"bytes"
	"context"
	"fmt"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
)

// jpegQuality is how well each thumbnail is kept when it is cut from its sheet: as well as Jellyfin keeps its sheets.
const jpegQuality = 90

func copyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "copy",
		Aliases: []string{"copy-from-jf"},
		Short:   "Make BIFs from Jellyfin's thumbnails, with no video decoded",
		Long: `Makes a BIF beside each video Jellyfin has made thumbnails of and that has none, cut from
Jellyfin's sheets of them: no video is read or decoded. The pictures are as wide as
Jellyfin's, 640 for most videos, and as far apart, every 10 seconds.

A video that already has a BIF is left as it is, unless --upgrade is given: then a BIF
narrower than Jellyfin's thumbnails, one that cannot be read, or one holding less than half
as many pictures, is replaced, and the BIFs it replaces are removed. A BIF ebb cut before is
cut again when Jellyfin has made its thumbnails again since.

Each BIF is written beside its video under a hidden name and renamed into place once whole,
so Emby never reads half of one. Every BIF written, replaced or removed is added to the record
(--record). --dry-run says what would be done and does nothing; --limit stops after so many.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			upgrade, _ := cmd.Flags().GetBool("upgrade")
			dry, _ := cmd.Flags().GetBool("dry-run")
			limit, _ := cmd.Flags().GetInt("limit")

			return runCopy(cmd, GetFlags(), upgrade, dry, limit)
		},
	}

	runFlags(cmd, "replace BIFs narrower than Jellyfin's thumbnails, or that cannot be read or hold too few pictures, and remove the ones replaced")

	return cmd
}

// runFlags adds the flags a command that writes BIFs has: --upgrade, saying what it replaces, --dry-run and --limit.
func runFlags(cmd *cobra.Command, upgrade string) {
	cmd.Flags().Bool("upgrade", false, upgrade)
	cmd.Flags().BoolP("dry-run", "n", false, "say what would be done, and do nothing")
	cmd.Flags().Int("limit", 0, "do at most this many videos: every one when 0")
}

// runCopy makes BIFs from Jellyfin's thumbnails.
func runCopy(cmd *cobra.Command, f *FlagData, upgrade, dry bool, limit int) error {
	if err := f.check(); err != nil {
		return err
	}
	naming, err := f.naming()
	if err != nil {
		return err
	}

	ctx, stop := signals(cmd)
	defer stop()

	s, err := f.open(ctx, true)
	if err != nil {
		return err
	}
	defer s.close()

	inv, err := f.take(ctx, s, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	var steps []plan.Step
	for _, v := range inv.Videos {
		if f.inScope(v) && v.Jellyfin != nil {
			steps = append(steps, plan.Copy(v, plan.CopyOptions{Naming: naming, Upgrade: upgrade, Changed: s.changed}))
		}
	}
	cout.Verbosef("%s\n", paint(quiet, number(len(steps))+" of the videos have Jellyfin thumbnails"))

	rec, err := f.record(dry)
	if err != nil {
		return err
	}
	defer func() { _ = rec.Close() }()

	t := carryOut(ctx, steps, f.Workers, limit, dry, job{write: s.cut, from: s.cutFrom}, rec)
	t.say(dry, rec)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stopped: %w", err)
	}

	return t.err()
}

// cutFrom is the set of Jellyfin's thumbnails a step's BIF is cut from, as the line that starts it says.
func (s *sources) cutFrom(step plan.Step) string {
	return fmt.Sprintf("Jellyfin's %s on %s %s", plural(step.From.Count, "thumbnail", "thumbnails"), plural(step.From.Sheets(), "sheet", "sheets"), paint(quiet, "in "+step.From.Folder(s.trickplay)))
}

// cut makes a step's BIF from Jellyfin's thumbnails, with a note in its first picture saying which set it was cut from and when that was made.
func (s *sources) cut(ctx context.Context, step plan.Step) (data []byte, target string, err error) {
	changed, err := s.changed(step.From)
	if err != nil {
		return nil, "", fmt.Errorf("looking at the thumbnails: %w", err)
	}

	frames, err := trickplay.Cut(ctx, s.trickplay, step.From, jpegQuality)
	if err != nil {
		return nil, "", err
	}
	frames[0] = bif.WithNote(frames[0], plan.CopyNote(step.From, changed))

	var b bytes.Buffer
	if err := bif.Write(&b, step.From.Interval, frames); err != nil {
		return nil, "", err
	}

	return b.Bytes(), step.Target, nil
}
