package verify_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/emby-bif-builder/lib/bif"
	"github.com/katbyte/emby-bif-builder/lib/inventory"
	"github.com/katbyte/emby-bif-builder/lib/verify"
)

// holding is a BIF of so many pictures ten seconds apart.
func holding(frames int) inventory.BIF {
	return inventory.BIF{Info: bif.Info{Frames: frames, Interval: 10 * time.Second, Width: 320, Height: 180}}
}

// A BIF is wrong when it cannot be read, or its pictures stop well before its video ends or run well past it. One that is a little out either way,
// as the last picture falls either side of the end, is fine, as is any BIF of a video Emby has no length for.
func TestWhatIsWrong(t *testing.T) {
	t.Parallel()

	broken := inventory.BIF{Err: errors.New("cut short: its pictures run to byte 900 but the file ends at 400")}
	for _, c := range []struct {
		name   string
		b      inventory.BIF
		length time.Duration
		kind   verify.Kind
		why    string
	}{
		{"broken", broken, time.Hour, verify.Broken, "cut short"},
		{"broken, no length", broken, 0, verify.Broken, "cut short"},
		{"as long as the video", holding(360), time.Hour, 0, ""},
		{"a picture past the end", holding(361), time.Hour - 5*time.Second, 0, ""},
		{"a few pictures short", holding(330), time.Hour, 0, ""},
		{"one picture of an hour", holding(1), time.Hour, verify.Short, "its 1 picture covers 10s, where Emby has the video as 1h 00m long"},
		{"stopped halfway", holding(180), time.Hour, verify.Short, "its 180 pictures cover 30m 00s, where Emby has the video as 1h 00m long"},
		{"of a longer cut", holding(540), time.Hour, verify.Long, "its 540 pictures cover 1h 30m, where Emby has the video as 1h 00m long"},
		{"one picture of a short clip", holding(1), 8 * time.Second, 0, ""},
		{"one picture of half a minute", holding(1), 30 * time.Second, verify.Short, "covers 10s, where Emby has the video as 30s long"},
		{"no length", holding(1), 0, 0, ""},
		{"every frame", holding(46486), 25*time.Minute + 51*time.Second, verify.Long, "its 46,486 pictures cover 129h 07m"},
		{"out of reach", inventory.BIF{Err: inventory.ErrUnreachable}, time.Hour, verify.Unopened, "cannot be opened from here"},
	} {
		p, wrong := verify.Check(c.b, c.length)
		if wrong != (c.kind != 0) || p.Kind != c.kind || !strings.Contains(p.Why, c.why) {
			t.Errorf("%s: %s (%q, %t), want %s saying %q", c.name, p.Kind, p.Why, wrong, c.kind, c.why)
		}
	}
}

// A length reads as a person says it.
func TestLength(t *testing.T) {
	t.Parallel()

	for d, want := range map[time.Duration]string{
		25 * time.Second:                           "25s",
		42*time.Minute + 10*time.Second:            "42m 10s",
		time.Hour + 2*time.Minute + 29*time.Second: "1h 02m",
		1500 * time.Millisecond:                    "2s",
	} {
		if got := verify.Length(d); got != want {
			t.Errorf("%s reads %q, want %q", d, got, want)
		}
	}
}
