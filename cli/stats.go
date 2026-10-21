package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/spf13/cobra"
)

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Count what has a BIF and what does not, library by library, and what Jellyfin has",
		Long: `Counts each library's films, shows, episodes and extras; how many of its videos have a BIF
beside them and how many do not; and how many of those Jellyfin has thumbnails of, which
copy would make a BIF for, or would replace a narrower one with, given --upgrade.

Then the BIFs themselves: how wide their pictures are, how they are named, who made them,
and those that cannot be read or hold a single picture. Then Jellyfin's thumbnails: how many
videos have them and how wide they are.

Extras (trailers, theme videos and the like) are counted, but left out of the rest unless
--extras is given. It reads every folder Emby's videos are in, which over a share takes a
few minutes, and changes nothing.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
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

			writeStats(cmd.OutOrStdout(), f, s, inv)

			return nil
		},
	}
}

// libraryCounts is one library's line of the table.
type libraryCounts struct {
	films, shows, episodes, extras int
	with, without                  int
	jellyfin, copyable, upgradable int
	gone                           int
}

// writeStats writes the counts.
func writeStats(w io.Writer, f *FlagData, s *sources, inv *inventory.Inventory) {
	byLib := map[string]*libraryCounts{}
	for _, l := range inv.Libraries {
		byLib[l.Name] = &libraryCounts{}
	}
	for _, sh := range inv.Shows {
		byLib[sh.Library].shows++
	}

	var (
		widths, names, makers = map[string]int{}, map[string]int{}, map[string]int{}
		bifs, unreadable      int
		single, several       int
		jfWidths              = map[int]int{}
		jfVideos, remakes     int
	)
	for _, v := range inv.Videos {
		c := byLib[v.Library]
		switch v.Kind {
		case emby.Film:
			c.films++
		case emby.Episode:
			c.episodes++
		default:
			c.extras++
		}
		if !f.inScope(v) {
			continue
		}

		switch {
		case v.Err != nil || !v.Found:
			c.gone++
		case len(v.BIFs) > 0:
			c.with++
		default:
			c.without++
		}

		bifs += len(v.BIFs)
		if len(v.BIFs) > 1 {
			several++
		}
		for _, b := range v.BIFs {
			names[nameOf(b)]++
			if b.Err != nil {
				unreadable++

				continue
			}
			widths[strconv.Itoa(b.Info.Width)]++
			makers[plan.Maker(b.Info.Note)]++
			if b.Info.Frames == 1 {
				single++
			}
		}

		if v.Jellyfin == nil {
			continue
		}
		jf, _ := v.Jellyfin.Widest()
		jfVideos++
		jfWidths[jf.Width]++
		c.jellyfin++

		// what copy would do, with leave to upgrade, so both counts come from the one rule
		switch plan.Copy(v, plan.CopyOptions{Upgrade: true, Changed: s.changed}).Action {
		case plan.Make:
			c.copyable++
		case plan.Upgrade:
			c.upgradable++
		case plan.Remake:
			remakes++
		case plan.Leave:
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintln(tw, "library\tfilms\tshows\tepisodes\textras\twith a BIF\twithout\tnot found\tJellyfin has\tto copy\tto upgrade\t")
	var total libraryCounts
	for _, l := range inv.Libraries {
		// a library of music or photos has none of what is counted
		c := byLib[l.Name]
		if c.films+c.shows+c.episodes+c.extras == 0 {
			continue
		}
		row(tw, l.Name, c)
		total.films, total.shows, total.episodes, total.extras = total.films+c.films, total.shows+c.shows, total.episodes+c.episodes, total.extras+c.extras
		total.with, total.without, total.gone = total.with+c.with, total.without+c.without, total.gone+c.gone
		total.jellyfin, total.copyable, total.upgradable = total.jellyfin+c.jellyfin, total.copyable+c.copyable, total.upgradable+c.upgradable
	}
	row(tw, "total", &total)
	_ = tw.Flush()

	scope := "films and episodes"
	if f.Extras {
		scope = "films, episodes and extras"
	}
	_, _ = fmt.Fprintf(w, "\nThe BIF and Jellyfin columns count %s. To copy is what copy would make a BIF for; to upgrade, what copy --upgrade would replace.\n", scope)
	if remakes > 0 {
		_, _ = fmt.Fprintf(w, "Jellyfin has also made its thumbnails again for %s that ebb cut BIFs of, which copy cuts again.\n", plural(remakes, "video", "videos"))
	}

	_, _ = fmt.Fprintf(w, "\nBIFs beside the %s: %s\n", scope, number(bifs))
	_, _ = fmt.Fprintf(w, "  by width:  %s\n", tallyOf(widths, 6, "widths"))
	_, _ = fmt.Fprintf(w, "  by name:   %s\n", tallyOf(names, 6, "names"))
	_, _ = fmt.Fprintf(w, "  made by:   %s\n", tallyOf(makers, 0, ""))
	_, _ = fmt.Fprintf(w, "  that cannot be read: %s; holding a single picture: %s; videos with more than one: %s\n", number(unreadable), number(single), number(several))

	var strayBytes int64
	for _, st := range inv.Strays {
		strayBytes += st.Bytes
	}
	_, _ = fmt.Fprintf(w, "  strays, beside no video of their name or half written: %s, %s; ebb strays lists them\n", number(len(inv.Strays)), size(strayBytes))

	if s.jellyfin == nil {
		_, _ = fmt.Fprintf(w, "\nJellyfin is left out: --jf-db-path (%s) is not set.\n", envNames["jf-db-path"])

		return
	}

	jw := map[string]int{}
	for width, n := range jfWidths {
		jw[strconv.Itoa(width)] = n
	}
	_, _ = fmt.Fprintf(w, "\nJellyfin has thumbnails of %s of these videos\n", number(jfVideos))
	_, _ = fmt.Fprintf(w, "  by width:  %s\n", tallyOf(jw, 6, "widths"))
	_, _ = fmt.Fprintf(w, "  and of %s videos in these libraries' folders that Emby does not have\n", number(len(inv.JellyfinOnly)))
}

// row writes one library's line.
func row(w io.Writer, name string, c *libraryCounts) {
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", name, number(c.films), number(c.shows), number(c.episodes), number(c.extras), number(c.with), number(c.without), number(c.gone), number(c.jellyfin), number(c.copyable), number(c.upgradable))
}

// nameOf is how a BIF is named, as the counts group them: -320-10, or bare.
func nameOf(b inventory.BIF) string {
	if b.Style == inventory.Bare {
		return "bare"
	}

	return "-" + b.Named
}

// tallyOf is counts as "320: 21,903, 640: 3", the most first. When most is above 0 only that many are named, and the rest are counted together as
// so many other of what: "26 other widths: 201".
func tallyOf(counts map[string]int, most int, what string) string {
	if len(counts) == 0 {
		return "none"
	}

	keys := slices.Collect(maps.Keys(counts))
	slices.SortFunc(keys, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}

		return strings.Compare(a, b)
	})

	parts := make([]string, 0, len(keys))
	rest := 0
	for i, k := range keys {
		if most > 0 && i >= most {
			rest += counts[k]

			continue
		}
		parts = append(parts, k+": "+number(counts[k]))
	}
	if left := len(keys) - len(parts); left > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s: %s", left, what, number(rest)))
	}

	return strings.Join(parts, ", ")
}
