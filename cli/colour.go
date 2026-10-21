package cli

import (
	"strings"
	"unicode/utf8"

	"github.com/gookit/color"
)

// The colours ebb speaks in. Each means the one thing wherever it is used, so a glance down the output says how things stand. They are tags that
// are turned into colour on a terminal and left out anywhere else.
const (
	// good is what is done or as it should be: a BIF made, a video that has one, nothing wrong.
	good = "green"
	// todo is what a run would do, or what is yet to be done: a BIF to copy or upgrade, a stray, something that cannot be told from here.
	todo = "yellow"
	// bad is what is wrong or failed: a broken BIF, a video that is not there, a write that failed.
	bad = "red"
	// quiet is what needs nothing: a video left as it is, a count of none, a line saying how far a run has got.
	quiet = "gray"
	// mark is what to look at: headings, the names of things, and the numbers in a line of progress.
	mark = "cyan"
)

// paint is s in the colour, or s as it is for no colour.
func paint(colour, s string) string {
	if colour == "" || s == "" {
		return s
	}

	return "<" + colour + ">" + s + "</>"
}

// amount is n as a person writes it, in the colour when there are any and quiet when there are none, so the counts that matter stand out.
func amount(n int, colour string) string {
	if n == 0 {
		return paint(quiet, "0")
	}

	return paint(colour, number(n))
}

// counted is so many of a thing, "3 strays", in the colour when there are any and quiet when there are none.
func counted(n int, one, many, colour string) string {
	if n == 0 {
		colour = quiet
	}

	return paint(colour, plural(n, one, many))
}

// shown is how many columns s takes on a terminal, its colour tags taking none.
func shown(s string) int {
	return utf8.RuneCountInString(color.ClearTag(s))
}

// table is rows of cells as columns two spaces apart, the first column to the left and the rest to the right, as a table of counts reads. A cell
// can be painted, its colour taking no room.
func table(rows [][]string) []string {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], shown(c))
		}
	}

	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-shown(c))
			if i == 0 {
				b.WriteString(c + pad)

				continue
			}
			b.WriteString("  " + pad + c)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}

	return lines
}
