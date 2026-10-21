package cli

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/place"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
)

func straysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "strays",
		Short: "List the BIFs whose video is no longer beside them, and remove them with --remove",
		Long: `Lists each BIF none of whose names could be a video's beside it: one left when its video was
renamed or removed, Emby's own or ebb's. Also each half-written BIF a run of ebb that was
stopped left behind, once it is an hour old.

A video is beside a BIF when a file of a video's kind has the name the BIF is named from, or
Emby has a video of that name there and it was found. Only the folders Emby's videos are in
are read: a folder whose every video is gone, and that Emby no longer has, is not.

--remove removes them, reading each folder again first so a video that has come back since
keeps its BIF, and adds each removal to the record (--record).`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			remove, _ := cmd.Flags().GetBool("remove")

			return runStrays(cmd, GetFlags(), remove)
		},
	}

	cmd.Flags().Bool("remove", false, "remove the strays found, adding each to the record")

	return cmd
}

// runStrays lists the strays, or removes them.
func runStrays(cmd *cobra.Command, f *FlagData, remove bool) error {
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

	var bytes int64
	for _, st := range inv.Strays {
		bytes += st.Bytes
	}
	if !remove {
		for _, st := range inv.Strays {
			cout.Printf("stray     %s: %s, %s\n", st.Path, st.Why, size(st.Bytes))
		}
		cout.Quietf("%s, %s; --remove removes them\n", plural(len(inv.Strays), "stray", "strays"), size(bytes))

		return nil
	}

	rec, err := f.record(false)
	if err != nil {
		return err
	}
	defer func() { _ = rec.Close() }()

	// each folder is read again just before its strays are removed, and only what is still a stray goes
	byFolder := inventory.ByFolder(inv.Videos)
	var removed, failed int
	var freed int64
	byDir := group(inv.Strays)
	for _, dir := range slices.Sorted(maps.Keys(byDir)) {
		strays := byDir[dir]
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stopped: %w", err)
		}

		still := map[string]bool{}
		for _, st := range inventory.Folder(ctx, dir, byFolder[dir]) {
			still[st.Path] = true
		}
		for _, st := range strays {
			if !still[st.Path] {
				cout.Verbosef("<gray>left</>      %s: no longer a stray\n", st.Path)

				continue
			}
			if err := place.Remove(st.Path, rec, "stray: "+st.Why); err != nil {
				failed++
				cout.Errorf("<red>failed</>    %s: %v\n", st.Path, err)

				continue
			}
			removed++
			freed += st.Bytes
			cout.Quietf("<yellow>removed</>   %s\n", st.Path)
		}
	}

	line := fmt.Sprintf("removed %s, %s", plural(removed, "stray", "strays"), size(freed))
	if rec != nil {
		line += "; every change is recorded in " + rec.Path
	}
	cout.Quietf("%s\n", line)
	if failed > 0 {
		return fmt.Errorf("%s could not be removed", plural(failed, "stray", "strays"))
	}

	return nil
}

// group is the strays by the folder each is in.
func group(strays []inventory.Stray) map[string][]inventory.Stray {
	byFolder := map[string][]inventory.Stray{}
	for _, st := range strays {
		dir := filepath.Dir(st.Path)
		byFolder[dir] = append(byFolder[dir], st)
	}

	return byFolder
}
