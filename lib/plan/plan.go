// Package plan decides what to do for each video: make it a BIF, replace one that is narrower, make one again from thumbnails Jellyfin has made
// again, or leave it be, and says why.
package plan

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/trickplay"
)

// Action is what is done for a video.
type Action int

const (
	// Leave is nothing: the video has what it needs, or something stands in the way.
	Leave Action = iota
	// Make is a BIF for a video with none.
	Make
	// Upgrade is a wider BIF in place of a narrower one, or of one that cannot be read or holds too few pictures.
	Upgrade
	// Remake is a BIF cut again, as Jellyfin has made the thumbnails ebb cut it from again since.
	Remake
)

// String is the action as a report says it.
func (a Action) String() string {
	switch a {
	case Leave:
		return "leave"
	case Make:
		return "make"
	case Upgrade:
		return "upgrade"
	case Remake:
		return "remake"
	default:
		return "unknown"
	}
}

// Reason is why a video is left as it is, for the totals.
type Reason int

const (
	// Done is a video that has what it needs.
	Done Reason = iota + 1
	// Held is a video whose BIF would be replaced with --upgrade.
	Held
	// Gone is a video that is not where Emby has it, or whose folder cannot be read.
	Gone
	// Elsewhere is a video Jellyfin has thumbnails of, which copy makes a BIF from rather than build.
	Elsewhere
	// Unsure is a video ebb could not tell the state of.
	Unsure
	// Unreachable is a video that is there but cannot be opened from here, nor a BIF written beside it under its name.
	Unreachable
)

// String is the reason as the totals say it.
func (r Reason) String() string {
	switch r {
	case Done:
		return "already have one"
	case Held:
		return "have one --upgrade would replace"
	case Gone:
		return "are not where Emby has them"
	case Elsewhere:
		return "have Jellyfin thumbnails, for copy"
	case Unsure:
		return "could not be told"
	case Unreachable:
		return "cannot be opened from here"
	default:
		return "unknown"
	}
}

// Naming is what ebb calls the BIFs it writes.
type Naming int

const (
	// NameEmby is <video>-320-10.bif, the name Emby gives its own, whatever the width inside, so Emby's own task counts the video as done.
	NameEmby Naming = iota
	// NameSize is <video>-640-10.bif, the width inside and the seconds apart.
	NameSize
	// NameBare is <video>.bif, as Roku players named them.
	NameBare
)

// Namings is each way of naming, as the --name setting spells it.
var Namings = map[string]Naming{"emby": NameEmby, "size": NameSize, "bare": NameBare}

// ParseNaming reads a way of naming as the --name setting spells it.
func ParseNaming(s string) (Naming, error) {
	n, ok := Namings[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return 0, fmt.Errorf("%q is not a way of naming: emby (<video>-320-10.bif), size (<video>-640-10.bif) or bare (<video>.bif)", s)
	}

	return n, nil
}

// Name is the path of the BIF ebb writes beside the video, for pictures this wide taken this far apart.
func Name(v *inventory.Video, n Naming, width int, interval time.Duration) string {
	dir, stem := filepath.Dir(v.Local), inventory.Stem(v.Local)
	seconds := int(interval.Round(time.Second) / time.Second)

	switch n {
	case NameSize:
		return filepath.Join(dir, fmt.Sprintf("%s-%d-%d.bif", stem, width, seconds))
	case NameBare:
		return filepath.Join(dir, stem+".bif")
	default:
		return filepath.Join(dir, fmt.Sprintf("%s-320-%d.bif", stem, seconds))
	}
}

// Step is what is to be done for one video.
type Step struct {
	Video  *inventory.Video
	Action Action
	// Why is a few words on why: what the video has, or what stands in the way. Reason is why it is left, for one that is.
	Why    string
	Reason Reason
	// Target is the BIF to write, and Replace the other BIFs beside the video, removed once it is in place.
	Target  string
	Replace []string
	// From is the set of Jellyfin's thumbnails to cut it from, when copying.
	From trickplay.Info
	// Width is the width to build it at, when building from the video.
	Width int
}

// CopyOptions is how a video's BIF is made from Jellyfin's thumbnails.
type CopyOptions struct {
	Naming Naming
	// Upgrade is leave to replace a BIF narrower than Jellyfin's thumbnails, or one that cannot be read or holds too few pictures.
	Upgrade bool
	// Changed is when a set's sheets were last written, to tell whether a BIF ebb cut was cut from them as they are now.
	Changed func(trickplay.Info) (time.Time, error)
}

// Copy is what to do for a video Jellyfin has made thumbnails of.
func Copy(v *inventory.Video, o CopyOptions) Step {
	s := Step{Video: v}
	if why, reason, ok := missing(v); ok {
		s.Why, s.Reason = why, reason

		return s
	}

	var jf trickplay.Info
	if v.Jellyfin != nil {
		jf, _ = v.Jellyfin.Widest()
	}
	if jf.Count == 0 {
		s.Why, s.Reason = "Jellyfin has no thumbnails of it", Unsure

		return s
	}

	s.From, s.Target = jf, Name(v, o.Naming, jf.Width, jf.Interval)
	s.Replace = Others(v, s.Target)
	best, readable := v.Widest()

	switch {
	case len(v.BIFs) == 0:
		s.Action, s.Why = Make, "no BIF"
	case !readable:
		upgrade(&s, o.Upgrade, "its BIF cannot be read")
	case best.Info.Width < jf.Width:
		upgrade(&s, o.Upgrade, fmt.Sprintf("its BIF is %d wide, Jellyfin's thumbnails %d", best.Info.Width, jf.Width))
	case best.Info.Frames*2 < jf.Count:
		upgrade(&s, o.Upgrade, fmt.Sprintf("its BIF holds %d pictures, Jellyfin has %d", best.Info.Frames, jf.Count))
	case isOurs(best.Info.Note, "jellyfin"):
		changed, err := o.Changed(jf)
		if err != nil {
			s.Why, s.Reason = fmt.Sprintf("cannot tell when Jellyfin made its thumbnails: %v", err), Unsure

			return s
		}

		if best.Info.Note != CopyNote(jf, changed) {
			s.Action, s.Why = Remake, "Jellyfin has made its thumbnails again since ebb cut its BIF"
		} else {
			s.Why, s.Reason = "has the BIF ebb cut from Jellyfin's thumbnails as they are", Done
		}
	default:
		s.Why, s.Reason = fmt.Sprintf("already has a BIF %d wide", best.Info.Width), Done
	}

	return s
}

// BuildOptions is how a video's BIF is built from the video itself.
type BuildOptions struct {
	Naming Naming
	// Upgrade is leave to replace a BIF narrower than Width, or one that cannot be read.
	Upgrade bool
	// Width is the width to build at, and Interval how far apart to take the pictures.
	Width    int
	Interval time.Duration
	// VideoWidth is the video's own width, 0 when that is not known, so no BIF is built wider than the video, or replaced by one no wider.
	VideoWidth func(*inventory.Video) int
}

// Build is what to do for a video when building BIFs from the videos themselves.
func Build(v *inventory.Video, o BuildOptions) Step {
	s := Step{Video: v, Width: o.Width}
	if why, reason, ok := missing(v); ok {
		s.Why, s.Reason = why, reason

		return s
	}
	if v.Jellyfin != nil {
		s.Why, s.Reason = "Jellyfin has thumbnails of it: copy makes its BIF from those", Elsewhere

		return s
	}

	s.Target = Name(v, o.Naming, o.Width, o.Interval)
	s.Replace = Others(v, s.Target)
	best, readable := v.Widest()

	switch {
	case len(v.BIFs) == 0:
		s.Action, s.Why = Make, "no BIF"
	case !readable:
		upgrade(&s, o.Upgrade, "its BIF cannot be read")
	case best.Info.Width < o.Width:
		// the video's own width is only asked for when the answer decides it, as each asking is a question of Emby's database
		if o.Upgrade && o.VideoWidth != nil {
			if w := o.VideoWidth(v); w > 0 && best.Info.Width >= w {
				s.Why, s.Reason = fmt.Sprintf("its BIF is %d wide, as wide as the video itself", best.Info.Width), Done

				return s
			}
		}
		upgrade(&s, o.Upgrade, fmt.Sprintf("its BIF is %d wide", best.Info.Width))
	default:
		s.Why, s.Reason = fmt.Sprintf("already has a BIF %d wide", best.Info.Width), Done
	}

	return s
}

// missing is why nothing can be done for a video that is not there, or cannot be reached from here, how the totals count it, and whether it is so.
func missing(v *inventory.Video) (string, Reason, bool) {
	switch {
	case v.Err != nil:
		return fmt.Sprintf("its folder cannot be read: %v", v.Err), Gone, true
	case !v.Found:
		return "the video is not where Emby has it", Gone, true
	case v.Unreachable:
		return "the video " + inventory.ErrUnreachable.Error(), Unreachable, true
	}

	return "", 0, false
}

// upgrade makes the step an Upgrade when there is leave to, and otherwise says that one would be.
func upgrade(s *Step, allowed bool, why string) {
	if allowed {
		s.Action, s.Why = Upgrade, why

		return
	}

	s.Why, s.Reason = why+": --upgrade replaces it", Held
}

// Others is every BIF beside the video but the one to be written at target.
func Others(v *inventory.Video, target string) []string {
	var paths []string
	for _, b := range v.BIFs {
		if b.Path != target {
			paths = append(paths, b.Path)
		}
	}

	return paths
}

// CopyNote is what ebb writes in a BIF cut from these thumbnails, whose sheets were last written then: enough to know, later, whether Jellyfin has
// made them again.
func CopyNote(i trickplay.Info, changed time.Time) string {
	return fmt.Sprintf("jellyfin %s %dx%d %d %d", i.ItemID, i.Width, i.Height, i.Count, changed.Unix())
}

// BuildNote is what ebb writes in a BIF it built from the video itself, at this width, from its key frames alone or from every frame.
func BuildNote(width int, keyframes bool) string {
	note := "ffmpeg " + strconv.Itoa(width)
	if keyframes {
		note += " keyframes"
	}

	return note
}

// isOurs says whether a note is one ebb wrote, of this kind.
func isOurs(note, kind string) bool {
	return strings.HasPrefix(note, kind+" ")
}

// Maker is who made a BIF, as a report says it: Jellyfin or ffmpeg by way of ebb, or someone else, Emby itself most likely.
func Maker(note string) string {
	switch {
	case isOurs(note, "jellyfin"):
		return "ebb, from Jellyfin"
	case isOurs(note, "ffmpeg"):
		return "ebb, from the video"
	default:
		return "Emby or another program"
	}
}
