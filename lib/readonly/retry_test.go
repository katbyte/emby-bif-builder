package readonly

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// A question that finds the file half changed is asked again until it is answered, or the tries run out; any other error is given back at once.
func TestAskingAgain(t *testing.T) {
	t.Parallel()

	damaged := fmt.Errorf("reading: %w", sqlite3.CORRUPT)
	asked := 0
	dropped := 0
	err := askAgain(t.Context(), 4, time.Millisecond, func() error {
		asked++
		if asked < 3 {
			return damaged
		}

		return nil
	}, func() { dropped++ })
	if err != nil || asked != 3 {
		t.Errorf("asked %d times, ending %v: want an answer on the third", asked, err)
	}
	if dropped != 2 {
		t.Errorf("dropped what was held of the file %d times, want once before each try after the first", dropped)
	}

	asked = 0
	if err := askAgain(t.Context(), 4, time.Millisecond, func() error { asked++; return damaged }, func() {}); !errors.Is(err, sqlite3.CORRUPT) || asked != 4 {
		t.Errorf("asked %d times, ending %v: want four tries and the damage said", asked, err)
	}

	asked = 0
	other := errors.New("no such table")
	if err := askAgain(t.Context(), 4, time.Millisecond, func() error { asked++; return other }, func() {}); !errors.Is(err, other) || asked != 1 {
		t.Errorf("asked %d times, ending %v: want one try for an error that asking again cannot mend", asked, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := askAgain(ctx, 4, time.Hour, func() error { return damaged }, func() {}); !errors.Is(err, context.Canceled) {
		t.Errorf("a run stopped while waiting ended %v", err)
	}
}
