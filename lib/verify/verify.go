// Package verify says what is wrong with a BIF: that it cannot be read, or that its pictures stop well before its video ends or run well past it.
package verify

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/inventory"
)

// Kind is what is wrong with a BIF.
type Kind int

const (
	// Broken is a BIF that cannot be read, or one of whose pictures cannot be shown.
	Broken Kind = iota + 1
	// Short is a BIF whose pictures stop well before its video does: a single picture for an hour of video, say.
	Short
	// Long is a BIF whose pictures run well past its video's end, as a BIF of another cut of the video would.
	Long
	// Unopened is a BIF that is listed but cannot be opened from here, so nothing is known of it: see inventory.ErrUnreachable.
	Unopened
)

func (k Kind) String() string {
	switch k {
	case Broken:
		return "broken"
	case Short:
		return "short"
	case Long:
		return "long"
	case Unopened:
		return "unopened"
	default:
		return "unknown"
	}
}

// Problem is what is wrong with one BIF.
type Problem struct {
	Kind Kind
	Why  string
}

// Check is what is wrong with the BIF, beside a video Emby read to be length long, and whether anything is. A length of 0 is one Emby has not read,
// and only whether the BIF can be read is then checked.
func Check(b inventory.BIF, length time.Duration) (Problem, bool) {
	switch {
	case errors.Is(b.Err, inventory.ErrUnreachable):
		return Problem{Kind: Unopened, Why: b.Err.Error()}, true
	case b.Err != nil:
		return Problem{Kind: Broken, Why: b.Err.Error()}, true
	}
	if length <= 0 || b.Info.Interval <= 0 {
		return Problem{}, false
	}

	covers := time.Duration(b.Info.Frames) * b.Info.Interval
	pictures := count(b.Info.Frames) + " pictures cover"
	if b.Info.Frames == 1 {
		pictures = "1 picture covers"
	}
	// the length is Emby's, which can be what is wrong
	why := fmt.Sprintf("its %s %s, where Emby has the video as %s long", pictures, Length(covers), Length(length))

	// a BIF can fall short of its video or run past it by a tenth of the video and a picture or two before it is wrong, as the last picture falls
	// either side of the end and the length Emby read can be a little out
	slack := length / 10
	switch {
	case covers < length-slack-b.Info.Interval:
		return Problem{Kind: Short, Why: why}, true
	case covers > length+slack+2*b.Info.Interval:
		return Problem{Kind: Long, Why: why}, true
	default:
		return Problem{}, false
	}
}

// Length is a length of video as a person says it: "1h 02m", "42m 10s", "25s".
func Length(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)

	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// count is n with commas between its thousands, as a person writes it.
func count(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}

	return s
}
