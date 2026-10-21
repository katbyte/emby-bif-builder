package plan_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/emby"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/jellyfin"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
)

// sheets is a set of Jellyfin's thumbnails, 640 wide, and when its sheets were written.
var (
	sheets  = trickplay.Info{ItemID: "CA2EA61E-9A85-5010-2B76-538C60E3CA05", Width: 640, Height: 360, Columns: 10, Rows: 10, Count: 241, Interval: 10 * time.Second}
	written = time.Date(2026, 10, 9, 3, 4, 48, 0, time.UTC)
)

// episode is a video beside which are BIFs of these widths, and which Jellyfin has the thumbnails of when thumbnails says so.
func episode(thumbnails bool, bifs ...inventory.BIF) *inventory.Video {
	v := &inventory.Video{ID: 7, Kind: emby.Episode, Path: "/mnt/video/tv/S/S - 01x07.mkv", Local: "/v/tv/S/S - 01x07.mkv", Found: true, BIFs: bifs}
	if thumbnails {
		v.Jellyfin = &jellyfin.Video{ID: sheets.ItemID, Path: v.Path, Thumbnails: []trickplay.Info{sheets}}
	}

	return v
}

// a is a BIF beside the episode of this name, width and number of pictures, with this note in it.
func a(name string, width, frames int, note string) inventory.BIF {
	return inventory.BIF{Path: "/v/tv/S/" + name, Name: name, Info: bif.Info{Frames: frames, Interval: 10 * time.Second, Width: width, Height: width * 9 / 16, Note: note}}
}

// changed is when the sheets were written, for the plan to ask.
func changed(trickplay.Info) (time.Time, error) { return written, nil }

// What copy does for each kind of video: make a BIF for one with none, replace a narrower one only with --upgrade, cut its own BIF again only when
// Jellyfin has made its thumbnails again, and leave the rest.
func TestWhatCopyDoes(t *testing.T) {
	t.Parallel()

	ours := plan.CopyNote(sheets, written)
	stale := plan.CopyNote(sheets, written.Add(-time.Hour))
	unreadable := inventory.BIF{Path: "/v/tv/S/S - 01x07-320-10.bif", Err: errors.New("cut short")}

	for name, c := range map[string]struct {
		v       *inventory.Video
		upgrade bool
		action  plan.Action
		reason  plan.Reason
		why     string
		replace int
	}{
		"none":                   {episode(true), false, plan.Make, 0, "no BIF", 0},
		"narrower":               {episode(true, a("S - 01x07-320-10.bif", 320, 241, "")), false, plan.Leave, plan.Held, "its BIF is 320 wide, Jellyfin's thumbnails 640: --upgrade replaces it", 0},
		"narrower, upgraded":     {episode(true, a("S - 01x07-320-10.bif", 320, 241, "")), true, plan.Upgrade, 0, "its BIF is 320 wide, Jellyfin's thumbnails 640", 0},
		"two, upgraded":          {episode(true, a("S - 01x07-320-10.bif", 320, 241, ""), a("S - 01x07.bif", 320, 241, "")), true, plan.Upgrade, 0, "320 wide", 1},
		"one picture":            {episode(true, a("S - 01x07-640-10.bif", 640, 1, "")), true, plan.Upgrade, 0, "holds 1 pictures, Jellyfin has 241", 1},
		"unreadable":             {episode(true, unreadable), false, plan.Leave, plan.Held, "cannot be read: --upgrade", 0},
		"unreadable, upgraded":   {episode(true, unreadable), true, plan.Upgrade, 0, "cannot be read", 0},
		"as wide":                {episode(true, a("S - 01x07-640-10.bif", 640, 241, "")), true, plan.Leave, plan.Done, "already has a BIF 640 wide", 0},
		"ours, as they are":      {episode(true, a("S - 01x07-320-10.bif", 640, 241, ours)), true, plan.Leave, plan.Done, "as they are", 0},
		"ours, made again since": {episode(true, a("S - 01x07-320-10.bif", 640, 241, stale)), false, plan.Remake, 0, "made its thumbnails again", 0},
		"no thumbnails":          {episode(false), true, plan.Leave, plan.Unsure, "Jellyfin has no thumbnails", 0},
	} {
		s := plan.Copy(c.v, plan.CopyOptions{Upgrade: c.upgrade, Changed: changed})
		// what would be replaced only matters to a video something is done for
		if s.Action == plan.Leave {
			s.Replace = nil
		}
		if s.Action != c.action || s.Reason != c.reason || !strings.Contains(s.Why, c.why) || len(s.Replace) != c.replace {
			t.Errorf("%s: %s (%s) %q replacing %q, want %s (%s) %q replacing %d", name, s.Action, s.Reason, s.Why, s.Replace, c.action, c.reason, c.why, c.replace)
		}
		if c.action != plan.Leave && (s.Target != "/v/tv/S/S - 01x07-320-10.bif" || s.From != sheets) {
			t.Errorf("%s: writes %s from %+v", name, s.Target, s.From)
		}
	}
}

// A video that is not there, or whose folder cannot be read, is left, and why is said.
func TestVideosThatAreNotThere(t *testing.T) {
	t.Parallel()

	gone := episode(true)
	gone.Found = false
	if s := plan.Copy(gone, plan.CopyOptions{Changed: changed}); s.Action != plan.Leave || s.Reason != plan.Gone {
		t.Errorf("a video not there: %s (%s)", s.Action, s.Reason)
	}

	unread := episode(true)
	unread.Err = errors.New("permission denied")
	if s := plan.Build(unread, plan.BuildOptions{Width: 640, Interval: 10 * time.Second}); s.Action != plan.Leave || !strings.Contains(s.Why, "permission denied") {
		t.Errorf("a video whose folder cannot be read: %s %q", s.Action, s.Why)
	}

	// a video out of reach is left by both, even with --upgrade and a BIF beside it that could not be read for being out of reach too
	far := episode(true, inventory.BIF{Path: "/v/tv/S/S - 01x07-320-10.bif", Err: inventory.ErrUnreachable})
	far.Unreachable = true
	if s := plan.Copy(far, plan.CopyOptions{Upgrade: true, Changed: changed}); s.Action != plan.Leave || s.Reason != plan.Unreachable || !strings.Contains(s.Why, "cannot be opened from here") {
		t.Errorf("copying a video out of reach: %s (%s) %q", s.Action, s.Reason, s.Why)
	}
	far.Jellyfin = nil
	if s := plan.Build(far, plan.BuildOptions{Upgrade: true, Width: 640, Interval: 10 * time.Second}); s.Action != plan.Leave || s.Reason != plan.Unreachable {
		t.Errorf("building a video out of reach: %s (%s) %q", s.Action, s.Reason, s.Why)
	}

	// when Jellyfin's sheets cannot be looked at, ebb's own BIF is left
	ours := episode(true, a("S - 01x07-320-10.bif", 640, 241, plan.CopyNote(sheets, written)))
	s := plan.Copy(ours, plan.CopyOptions{Changed: func(trickplay.Info) (time.Time, error) { return time.Time{}, errors.New("gone") }})
	if s.Action != plan.Leave || s.Reason != plan.Unsure {
		t.Errorf("sheets that cannot be looked at: %s (%s)", s.Action, s.Reason)
	}
}

// What build does: leave videos Jellyfin has to copy, make a BIF for one with none, and replace a narrower one only with --upgrade and only when
// the video is wider.
func TestWhatBuildDoes(t *testing.T) {
	t.Parallel()

	asked := 0
	width := func(w int) func(*inventory.Video) int {
		return func(*inventory.Video) int { asked++; return w }
	}

	for name, c := range map[string]struct {
		v       *inventory.Video
		upgrade bool
		video   int
		action  plan.Action
		why     string
	}{
		"Jellyfin has it":         {episode(true), true, 1920, plan.Leave, "copy makes its BIF"},
		"none":                    {episode(false), false, 1920, plan.Make, "no BIF"},
		"narrower":                {episode(false, a("S - 01x07-320-10.bif", 320, 241, "")), false, 1920, plan.Leave, "320 wide: --upgrade replaces it"},
		"narrower, upgraded":      {episode(false, a("S - 01x07-320-10.bif", 320, 241, "")), true, 1920, plan.Upgrade, "320 wide"},
		"as wide as the video":    {episode(false, a("S - 01x07-320-10.bif", 320, 241, "")), true, 320, plan.Leave, "as wide as the video itself"},
		"video's width not known": {episode(false, a("S - 01x07-320-10.bif", 320, 241, "")), true, 0, plan.Upgrade, "320 wide"},
		"as wide":                 {episode(false, a("S - 01x07-640-10.bif", 640, 241, "")), true, 1920, plan.Leave, "already has a BIF 640 wide"},
	} {
		s := plan.Build(c.v, plan.BuildOptions{Upgrade: c.upgrade, Width: 640, Interval: 10 * time.Second, VideoWidth: width(c.video)})
		if s.Action != c.action || !strings.Contains(s.Why, c.why) {
			t.Errorf("%s: %s %q, want %s %q", name, s.Action, s.Why, c.action, c.why)
		}
	}
	if asked != 3 {
		t.Errorf("the video's width was asked %d times, want only for the 3 that would be upgraded", asked)
	}
}

// The BIFs written are named as Emby names its own unless --name says otherwise, and the ways of naming are read whatever their case.
func TestNames(t *testing.T) {
	t.Parallel()

	v := episode(false)
	for naming, want := range map[plan.Naming]string{plan.NameEmby: "/v/tv/S/S - 01x07-320-10.bif", plan.NameSize: "/v/tv/S/S - 01x07-544-10.bif", plan.NameBare: "/v/tv/S/S - 01x07.bif"} {
		if got := plan.Name(v, naming, 544, 10*time.Second); got != want {
			t.Errorf("named %s, want %s", got, want)
		}
	}

	if n, err := plan.ParseNaming(" Size "); err != nil || n != plan.NameSize {
		t.Errorf("Size reads as %d (%v)", n, err)
	}
	if _, err := plan.ParseNaming("640"); err == nil {
		t.Error("a way of naming ebb does not know was taken")
	}
}

// Who made a BIF is read from ebb's note in it.
func TestWhoMadeIt(t *testing.T) {
	t.Parallel()

	for note, want := range map[string]string{plan.CopyNote(sheets, written): "ebb, from Jellyfin", plan.BuildNote(640, true): "ebb, from the video", "": "Emby or another program"} {
		if got := plan.Maker(note); got != want {
			t.Errorf("%q was made by %q, want %q", note, got, want)
		}
	}
	if plan.BuildNote(640, false) != "ffmpeg 640" || plan.BuildNote(320, true) != "ffmpeg 320 keyframes" {
		t.Error("the notes of a built BIF are wrong")
	}
}

// A BIF's proper name says what it holds, the width of its pictures and the seconds between them, keeping the video's part of its name as it is.
func TestProperName(t *testing.T) {
	t.Parallel()

	held := func(name string, style inventory.Style, named string, width int, interval time.Duration) inventory.BIF {
		return inventory.BIF{Path: "/v/tv/S/" + name, Name: name, Style: style, Named: named, Info: bif.Info{Frames: 9, Interval: interval, Width: width}}
	}
	for _, c := range []struct {
		b    inventory.BIF
		want string
	}{
		{held("S - 01x07-320-10.bif", inventory.Sized, "320-10", 640, 10*time.Second), "/v/tv/S/S - 01x07-640-10.bif"},
		{held("S - 01x07.bif", inventory.Bare, "", 576, 10*time.Second), "/v/tv/S/S - 01x07-576-10.bif"},
		{held("S - 01x07-640-10.bif", inventory.Sized, "640-10", 640, 10*time.Second), "/v/tv/S/S - 01x07-640-10.bif"},
		{held("S - 01x07-320-10.bif", inventory.Sized, "320-10", 320, 5*time.Second), "/v/tv/S/S - 01x07-320-5.bif"},
		{held("2020-10-05.bif", inventory.Bare, "", 640, 10*time.Second), "/v/tv/S/2020-10-05-640-10.bif"},
		{held("S - 01x07-320-10.bif", inventory.Sized, "320-10", 640, 500*time.Millisecond), ""},
		{inventory.BIF{Path: "/v/tv/S/S - 01x07-320-10.bif", Name: "S - 01x07-320-10.bif", Err: errors.New("cut short")}, ""},
	} {
		if got := plan.ProperName(c.b); got != c.want {
			t.Errorf("%s holding %dx at %s is properly %q, want %q", c.b.Name, c.b.Info.Width, c.b.Info.Interval, got, c.want)
		}
	}
}
