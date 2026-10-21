package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gookit/color"
	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
	"github.com/spf13/cobra"
)

func lookupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lookup <video, BIF, Jellyfin item ID or trickplay folder>...",
		Short: "Say what a video, a BIF or a Jellyfin thumbnail folder is, and what each of the others has for it",
		Long: `Says, for each thing named, what video it is and what that video has: where it is, what
Emby takes it for and which library holds it, the BIFs beside it and what each holds, the
thumbnails Jellyfin has made of it and where their sheets are, and what copy would do.

A thing is named by any of: the video, as the servers see it or as it is found here; a BIF
beside it; Jellyfin's item ID, with or without its dashes; or a folder or sheet in Jellyfin's
trickplay folder, which is named by the ID. It changes nothing.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
			s, err := f.open(cmd.Context(), false)
			if err != nil {
				return err
			}
			defer s.close()

			libs, err := s.emby.Libraries(cmd.Context())
			if err != nil {
				return err
			}

			var failed int
			for i, arg := range args {
				if i > 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout())
				}

				path, err := s.resolve(cmd.Context(), libs, arg)
				if err == nil {
					err = s.describe(cmd.Context(), cmd.OutOrStdout(), libs, path)
				}
				if err != nil {
					failed++
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n  %v\n", arg, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%s could not be looked up", plural(failed, "thing", "things"))
			}

			return nil
		},
	}
}

// resolve is the video a thing names, as the servers see it: a Jellyfin item ID or a trickplay folder for Jellyfin's video, a BIF for the video
// beside it, and a path for the video, whether as the servers see it or as it is found here.
func (s *sources) resolve(ctx context.Context, libs []emby.Library, arg string) (string, error) {
	if id, ok := trickplay.ItemID(arg); ok {
		if s.jellyfin == nil {
			return "", fmt.Errorf("looking up a Jellyfin item needs --jf-db-path (%s) set", envNames["jf-db-path"])
		}

		v, found, err := s.jellyfin.ByID(ctx, id)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("there is no item %s in Jellyfin", id)
		}

		return v.Path, nil
	}

	path := filepath.ToSlash(arg)
	if !strings.HasPrefix(path, "/") {
		if abs, err := filepath.Abs(arg); err == nil {
			path = filepath.ToSlash(abs)
		}
	}
	// a path the servers know is one as they see it; any other is one as it is found here
	if _, found, err := s.emby.ByPath(ctx, libs, path); err != nil || found {
		return path, err
	}
	server := s.paths.Server(path)

	stem, ok := strings.CutSuffix(filepath.Base(server), ".bif")
	if !ok {
		return server, nil
	}

	// a BIF is named from its video's name, with the video's extension left off: the video is the file beside it Emby has, or failing that, the
	// first file beside it of that name
	dir := filepath.Dir(server)
	entries, err := os.ReadDir(s.paths.Local(dir))
	if err != nil {
		return "", err
	}
	var first string
	for _, st := range inventory.Stems(stem) {
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".bif") || inventory.Stem(e.Name()) != st {
				continue
			}

			candidate := dir + "/" + e.Name()
			if _, found, err := s.emby.ByPath(ctx, libs, candidate); err == nil && found {
				return candidate, nil
			}
			if first == "" {
				first = candidate
			}
		}
	}
	if first == "" {
		return "", errors.New("there is no video beside this BIF of the name it is named from")
	}

	return first, nil
}

// describe writes what the video at path has, each finding in the colour of what it means.
func (s *sources) describe(ctx context.Context, w io.Writer, libs []emby.Library, path string) error {
	color.Fprintln(w, paint(mark, path))
	label := func(name string) string { return paint(quiet, fmt.Sprintf("  %-12s", name)) }

	v := &inventory.Video{Path: path, Local: s.paths.Local(path)}
	if v.Local != path {
		color.Fprintf(w, "%s %s\n", paint(quiet, "  found here at"), v.Local)
	}

	item, found, err := s.emby.ByPath(ctx, libs, path)
	if err != nil {
		return err
	}
	if found {
		v.Item = item
		color.Fprintf(w, "%s%s %d, in %s\n", label("Emby:"), item.Kind, item.ID, orNone(item.Library, paint(todo, "no library")))
	} else {
		color.Fprintf(w, "%s%s\n", label("Emby:"), paint(todo, "does not have it"))
	}

	_ = inventory.Folder(ctx, filepath.Dir(v.Local), []*inventory.Video{v})
	switch {
	case v.Err != nil:
		color.Fprintf(w, "%s%s\n", label("video:"), paint(bad, fmt.Sprintf("its folder cannot be read: %v", v.Err)))
	case !v.Found:
		color.Fprintf(w, "%s%s\n", label("video:"), paint(bad, "is not there"))
	case v.Unreachable:
		color.Fprintf(w, "%s%s\n", label("video:"), paint(todo, inventory.ErrUnreachable.Error()))
	}

	var jf jellyfin.Video
	if s.jellyfin != nil {
		if jf, found, err = s.jellyfin.ByPath(ctx, path); err != nil {
			return err
		}
	}
	if v.ID == 0 && !v.Found && jf.ID == "" {
		return errors.New("nothing has it: not Emby, not Jellyfin, and there is no such file")
	}
	if len(v.BIFs) == 0 {
		color.Fprintf(w, "%s%s\n", label("BIF:"), paint(todo, "none"))
	}
	for _, b := range v.BIFs {
		color.Fprintf(w, "%s%s\n", label("BIF:"), b.Name)
		switch {
		case errors.Is(b.Err, inventory.ErrUnreachable):
			color.Fprintf(w, "%s%s\n", label(""), paint(todo, "cannot be opened from here"))
		case b.Err != nil:
			color.Fprintf(w, "%s%s\n", label(""), paint(bad, fmt.Sprintf("cannot be read: %v", b.Err)))
		default:
			color.Fprintf(w, "%s%dx%d, %s %s apart, %s, written %s, made by %s\n", label(""), b.Info.Width, b.Info.Height, plural(b.Info.Frames, "picture", "pictures"), b.Info.Interval, size(b.Bytes), b.Changed.Format(time.DateTime), plan.Maker(b.Info.Note))
		}
	}

	if s.jellyfin == nil {
		return nil
	}
	if !found {
		color.Fprintf(w, "%s%s\n", label("Jellyfin:"), paint(todo, "does not have it"))

		return nil
	}
	color.Fprintf(w, "%s%s %s\n", label("Jellyfin:"), strings.ToLower(jf.Type), jf.ID)
	if len(jf.Thumbnails) == 0 {
		color.Fprintf(w, "%s%s\n", label("thumbnails:"), paint(todo, "none yet"))

		return nil
	}
	for _, i := range jf.Thumbnails {
		color.Fprintf(w, "%s%dx%d, %d of them %s apart, on sheets of %dx%d\n", label("thumbnails:"), i.Width, i.Height, i.Count, i.Interval, i.Columns, i.Rows)
		if s.trickplay != "" {
			color.Fprintf(w, "%s%s %s: %s\n", label(""), paint(quiet, "in"), i.Folder(s.trickplay), s.sheets(i))
		}
	}

	if s.trickplay != "" {
		v.Jellyfin = &jf
		for _, upgrade := range []bool{false, true} {
			st := plan.Copy(v, plan.CopyOptions{Upgrade: upgrade, Changed: s.changed})
			flag := ""
			if upgrade {
				flag = " --upgrade"
			}
			color.Fprintf(w, "  copy%s would %s: %s\n", flag, paint(stepColour(st), st.Action.String()), st.Why)
		}
	}

	return nil
}

// stepColour is the colour of what a run would do for a video: good when it has what it needs, to do when the run or --upgrade would see to it,
// and bad when it cannot be done.
func stepColour(st plan.Step) string {
	switch {
	case st.Action != plan.Leave:
		return todo
	case st.Reason == plan.Done:
		return good
	case st.Reason == plan.Gone:
		return bad
	default:
		return todo
	}
}

// sheets says how many of a set's sheets are there, and when they were written.
func (s *sources) sheets(i trickplay.Info) string {
	there := 0
	for n := range i.Sheets() {
		if _, err := os.Stat(filepath.Join(i.Folder(s.trickplay), strconv.Itoa(n)+".jpg")); err == nil {
			there++
		}
	}

	colour := good
	if there < i.Sheets() {
		colour = bad
	}
	said := paint(colour, fmt.Sprintf("%d of %d sheets there", there, i.Sheets()))
	if changed, err := s.changed(i); err == nil {
		said += ", written " + changed.Format(time.DateTime)
	}

	return said
}

// size is a number of bytes as a person says it.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// orNone is s, or what to say in its place when it is empty.
func orNone(s, none string) string {
	if s == "" {
		return none
	}

	return s
}
