package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
)

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "List the videos behind one of the counts stats gives",
		Long: `Lists the videos behind one of the counts stats gives, one a line, so each can be
looked at: not-found lists the videos Emby has whose file is not there.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "not-found",
		Short: "List the videos Emby has whose file is not there",
		Long: `Lists each video Emby has whose file is not where Emby has it, and why: its folder is
there but the file is not, most often a video renamed or removed since Emby last read
the library, or the folder is gone too or cannot be read. Then how many, library by
library.

These are the videos stats counts as not found, and copy and build leave alone. A
library that Emby has read since they went no longer has them. Changes nothing.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			return runNotFound(cmd, GetFlags())
		},
	})

	return cmd
}

// runNotFound lists the videos Emby has whose file is not there.
func runNotFound(cmd *cobra.Command, f *FlagData) error {
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

	byLib := map[string]int{}
	for _, v := range inv.Videos {
		if !f.inScope(v) || (v.Err == nil && v.Found) {
			continue
		}

		byLib[v.Library]++
		cout.Printf("%s %s: %s; %s\n", paint(bad, "not found"), v.Local, whyNotFound(v), paint(quiet, fmt.Sprintf("Emby has it as %s %d in %s", v.Kind, v.ID, v.Library)))
	}

	libs := slices.Sorted(maps.Keys(byLib))
	slices.SortStableFunc(libs, func(a, b string) int { return byLib[b] - byLib[a] })
	in := make([]string, 0, len(libs))
	total := 0
	for _, l := range libs {
		in = append(in, number(byLib[l])+" in "+l)
		total += byLib[l]
	}

	line := counted(total, "video", "videos", bad) + " not found"
	if len(in) > 0 {
		line += ": " + strings.Join(in, ", ")
	}
	cout.Quietf("%s\n", line)

	return nil
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
