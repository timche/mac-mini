package status

import (
	"strings"
)

// The whole of the layout: columns as wide as their widest cell and two spaces between them,
// so a screen read at a glance has its states under one another. Written out by hand rather
// than with text/tabwriter, because the one thing a column of this is allowed to be is
// coloured, and an escape code is bytes tabwriter would count as width — a row with a warning
// in it would be the one row out of line.
type table struct {
	rows [][]cell
}

// A cell, and whether what it says is something being wrong. Nothing else decides a colour:
// what is already fine does not need pointing out, and a screen where everything is coloured
// is a screen where nothing is.
type cell struct {
	text string
	warn bool
}

func plain(text string) cell { return cell{text: text} }
func wrong(text string) cell { return cell{text: text, warn: true} }
func maybe(text string, bad bool) cell {
	return cell{text: text, warn: bad}
}

func (t *table) add(cells ...cell) { t.rows = append(t.rows, cells) }

func (t *table) empty() bool { return len(t.rows) == 0 }

// Two spaces of indent under the section's name, and the trailing spaces of the last column
// taken off: a line that ends in padding is a line that looks ragged the moment anybody
// selects it.
func (t *table) String(paint func(cell) string) string {
	widths := map[int]int{}
	for _, row := range t.rows {
		for i, last := 0, lastFilled(row); i < last; i++ {
			if len(row[i].text) > widths[i] {
				widths[i] = len(row[i].text)
			}
		}
	}

	var out strings.Builder
	for _, row := range t.rows {
		out.WriteString("  ")

		last := lastFilled(row)
		for i := 0; i <= last; i++ {
			out.WriteString(paint(row[i]))
			if i < last {
				out.WriteString(strings.Repeat(" ", widths[i]-len(row[i].text)+2))
			}
		}
		out.WriteString("\n")
	}
	return out.String()
}

// How far along the row anything is actually said. A column is empty in a row whenever the
// thing it is about does not apply — an agent with no threshold, a repository that is not
// paused — and the padding before an empty cell is trailing whitespace on the line.
func lastFilled(row []cell) int {
	last := len(row) - 1
	for last > 0 && row[last].text == "" {
		last--
	}
	return last
}

// Yellow and nothing else. A terminal gets one colour because one is all the screen means by
// it — this is degraded — and a pipe gets none, since the first thing anybody does with the
// output is grep it or paste it into a message.
func painter(colour bool) func(cell) string {
	return func(c cell) string {
		if !colour || !c.warn || c.text == "" {
			return c.text
		}
		return "\x1b[33m" + c.text + "\x1b[0m"
	}
}
