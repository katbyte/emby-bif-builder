package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gookit/color"
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
		unopened              int
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
			if errors.Is(b.Err, inventory.ErrUnreachable) {
				unopened++

				continue
			}
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

	head := []string{"library", "films", "shows", "episodes", "extras", "with a BIF", "without", "not found", "Jellyfin has", "to copy", "to upgrade"}
	for i, h := range head {
		head[i] = paint(mark, h)
	}
	rows := [][]string{head}
	var total libraryCounts
	for _, l := range inv.Libraries {
		// a library of music or photos has none of what is counted
		c := byLib[l.Name]
		if c.films+c.shows+c.episodes+c.extras == 0 {
			continue
		}
		rows = append(rows, row(l.Name, c))
		total.films, total.shows, total.episodes, total.extras = total.films+c.films, total.shows+c.shows, total.episodes+c.episodes, total.extras+c.extras
		total.with, total.without, total.gone = total.with+c.with, total.without+c.without, total.gone+c.gone
		total.jellyfin, total.copyable, total.upgradable = total.jellyfin+c.jellyfin, total.copyable+c.copyable, total.upgradable+c.upgradable
	}
	rows = append(rows, row(paint(mark, "total"), &total))
	for _, line := range table(rows) {
		color.Fprintln(w, line)
	}

	scope := "films and episodes"
	if f.Extras {
		scope = "films, episodes and extras"
	}
	color.Fprintln(w, paint(quiet, "\nThe BIF and Jellyfin columns count "+scope+". To copy is what copy would make a BIF for; to upgrade, what copy --upgrade would replace."))
	if total.gone > 0 {
		color.Fprintln(w, paint(quiet, "ebb show not-found lists the videos not found."))
	}
	if remakes > 0 {
		color.Fprintf(w, "Jellyfin has also made its thumbnails again for %s that ebb cut BIFs of, which copy cuts again.\n", counted(remakes, "video", "videos", todo))
	}

	// a width is good once it is as wide as ebb makes them, and one to upgrade when it is narrower
	byWidth := func(w string) string {
		if n, err := strconv.Atoi(w); err == nil && n >= f.Size {
			return good
		}

		return todo
	}
	color.Fprintf(w, "\n%s %s\n", paint(mark, "BIFs beside the "+scope+":"), number(bifs))
	color.Fprintf(w, "  by width:  %s\n", tallyOf(widths, 6, "widths", byWidth))
	color.Fprintf(w, "  by name:   %s\n", tallyOf(names, 6, "names", nil))
	color.Fprintf(w, "  made by:   %s\n", tallyOf(makers, 0, "", nil))
	color.Fprintf(w, "  that cannot be read: %s; holding a single picture: %s; videos with more than one: %s; %s\n", amount(unreadable, bad), amount(single, todo), amount(several, todo), paint(quiet, "ebb verify lists those that are wrong"))
	if unopened > 0 {
		color.Fprintf(w, "  that cannot be opened from here, as a Mac cannot open a name on a share whose accents are kept as separate characters: %s; %s\n", amount(unopened, todo), paint(quiet, "run ebb where the files are to read them"))
	}

	var strayBytes int64
	for _, st := range inv.Strays {
		strayBytes += st.Bytes
	}
	color.Fprintf(w, "  strays, beside no video of their name or half written: %s, %s; %s\n", amount(len(inv.Strays), todo), size(strayBytes), paint(quiet, "ebb strays lists them"))

	if s.jellyfin == nil {
		color.Fprintf(w, "\n%s\n", paint(todo, fmt.Sprintf("Jellyfin is left out: --jf-db-path (%s) is not set.", envNames["jf-db-path"])))

		return
	}

	jw := map[string]int{}
	for width, n := range jfWidths {
		jw[strconv.Itoa(width)] = n
	}
	color.Fprintf(w, "\n%s %s %s\n", paint(mark, "Jellyfin has thumbnails of"), number(jfVideos), paint(mark, "of these videos"))
	color.Fprintf(w, "  by width:  %s\n", tallyOf(jw, 6, "widths", byWidth))
	color.Fprintf(w, "  and of %s videos in these libraries' folders that Emby does not have\n", amount(len(inv.JellyfinOnly), todo))
}

// row is one library's line of the table, each count in the colour of what it means.
func row(name string, c *libraryCounts) []string {
	return []string{name, amount(c.films, ""), amount(c.shows, ""), amount(c.episodes, ""), amount(c.extras, ""), amount(c.with, good), amount(c.without, todo), amount(c.gone, bad), amount(c.jellyfin, ""), amount(c.copyable, todo), amount(c.upgradable, todo)}
}

// nameOf is how a BIF is named, as the counts group them: -320-10, or bare.
func nameOf(b inventory.BIF) string {
	if b.Style == inventory.Bare {
		return "bare"
	}

	return "-" + b.Named
}

// tallyOf is counts as "320: 21,903, 640: 3", the most first. When most is above 0 only that many are named, and the rest are counted together as
// so many other of what: "26 other widths: 201". colourOf, when given, is the colour each is named in.
func tallyOf(counts map[string]int, most int, what string, colourOf func(string) string) string {
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
		key := k
		if colourOf != nil {
			key = paint(colourOf(k), k)
		}
		parts = append(parts, key+": "+number(counts[k]))
	}
	if left := len(keys) - len(parts); left > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s: %s", left, what, number(rest)))
	}

	return strings.Join(parts, ", ")
}
