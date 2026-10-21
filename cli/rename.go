package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/place"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
)

func renameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Rename each BIF for what it holds: <video>-<width>-<seconds>.bif",
		Long: `Renames each BIF beside Emby's videos whose name does not say what it holds, to
<video>-<width>-<seconds>.bif: how wide its pictures are and how many seconds apart, read
from the BIF itself. A -320-10.bif holding pictures 640 wide becomes -640-10.bif, and a
bare <video>.bif gets its width and seconds.

A BIF that cannot be read, or cannot be opened from here, is left as it is, as is one
whose new name another BIF beside it already has. Only the name changes, and each
renaming is added to the record (--record). --dry-run says what would be renamed and
renames nothing.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			dry, _ := cmd.Flags().GetBool("dry-run")
			limit, _ := cmd.Flags().GetInt("limit")

			return runRename(cmd, GetFlags(), dry, limit)
		},
	}

	cmd.Flags().BoolP("dry-run", "n", false, "say what would be renamed, and rename nothing")
	cmd.Flags().Int("limit", 0, "rename no more than this many, 0 for all")

	return cmd
}

// renaming is one BIF to rename, and what to.
type renaming struct {
	from, to string
}

// runRename renames each BIF whose name does not say what it holds, or says what it would rename.
func runRename(cmd *cobra.Command, f *FlagData, dry bool, limit int) error {
	if err := f.check(); err != nil {
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
		doing []renaming
		taken int
	)
	for _, v := range inv.Videos {
		if !f.inScope(v) || v.Unreachable {
			continue
		}

		// a new name is only given where no BIF beside the video has it, nor one already renamed to it
		names := map[string]bool{}
		for _, b := range v.BIFs {
			names[b.Name] = true
		}
		for _, b := range v.BIFs {
			to := plan.ProperName(b)
			if to == "" || filepath.Base(to) == b.Name {
				continue
			}
			if names[filepath.Base(to)] {
				taken++
				cout.Verbosef("%s %s: %s\n", paint(quiet, "left     "), named(b.Path), paint(quiet, filepath.Base(to)+" is already there"))

				continue
			}
			names[filepath.Base(to)] = true
			doing = append(doing, renaming{from: b.Path, to: to})
		}
	}
	if limit > 0 && len(doing) > limit {
		doing = doing[:limit]
	}

	rec, err := f.record(dry)
	if err != nil {
		return err
	}
	defer func() { _ = rec.Close() }()

	of := number(len(doing))
	under := strings.Repeat(" ", len(of)*2+3)
	var renamed, failed int
	for i, r := range doing {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stopped: %w", err)
		}

		at := paint(progress(i+1, len(doing)), fmt.Sprintf("[%*s/%s]", len(of), number(i+1), of))
		rest := fmt.Sprintf("%s %s %s\n%s %s %s\n", under, paint(quiet, fmt.Sprintf("%-12s", "to")), paint("bold", filepath.Base(r.to)), under, paint(quiet, fmt.Sprintf("%-12s", "in")), paint(quiet, filepath.Dir(r.from)))
		if dry {
			cout.Printf("%s %s %s\n%s", at, paint(todo, "would rename"), filepath.Base(r.from), rest)

			continue
		}

		if err := place.Rename(r.from, r.to, rec, "renamed from "+filepath.Base(r.from)+", for what it holds"); err != nil {
			failed++
			cout.Errorf("%s %s %s: %s\n", at, paint(bad, fmt.Sprintf("%-12s", "failed")), filepath.Base(r.from), paint(bad, err.Error()))

			continue
		}
		renamed++
		cout.Quietf("%s %s %s\n%s", at, paint(good, fmt.Sprintf("%-12s", "renamed")), filepath.Base(r.from), rest)
	}

	switch {
	case dry:
		cout.Quietf("would rename %s\n", counted(len(doing), "BIF", "BIFs", todo))
	default:
		line := fmt.Sprintf("renamed %s, failed %s", counted(renamed, "BIF", "BIFs", good), amount(failed, bad))
		if rec != nil {
			line += paint(quiet, "; every change is recorded in "+rec.Path)
		}
		cout.Quietf("%s\n", line)
	}
	if taken > 0 {
		cout.Printf("%s\n", paint(todo, fmt.Sprintf("left %s whose new name another BIF beside the video already has", plural(taken, "BIF", "BIFs"))))
	}
	if failed > 0 {
		return fmt.Errorf("%s could not be renamed", plural(failed, "BIF", "BIFs"))
	}

	return nil
}

// misnamed says whether a BIF's name does not say what it holds, which rename would put right.
func misnamed(b inventory.BIF) bool {
	to := plan.ProperName(b)

	return to != "" && filepath.Base(to) != b.Name
}
